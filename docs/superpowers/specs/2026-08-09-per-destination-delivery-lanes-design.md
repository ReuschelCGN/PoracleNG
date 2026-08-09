# Per-Destination Delivery Lanes — Design

Status: draft / for review
Date: 2026-08-09
Supersedes: PR #182 (interim per-channel serialization + 429 backoff — the redesign carries its fix)

## Summary

Replace the delivery `FairQueue`'s **single shared job channel + shared worker
pool + per-destination `sync.Mutex`** with **one lightweight queue ("lane") and
one drainer goroutine per destination**. Lanes are spawned on demand and reaped
when idle. A global per-platform semaphore still caps total concurrent API
calls. This isolates routes: a rate-limited or hot destination drains at its own
pace on its own lane without tying up shared workers or the shared buffer, so
every other route keeps flowing.

## Background: why the shared model degrades globally

Today (`internal/delivery/queue.go`):
- **One** channel `ch` (buffer `delivery_queue_size`, default **200**) carries
  every job for every destination and platform.
- Workers = sum of per-platform concurrency
  (`concurrent_discord_destinations`=10 + `concurrent_discord_webhooks`=10 +
  `concurrent_telegram_destinations`=10 = **30** default) all pull from `ch`.
- `processJob` acquires the **per-destination lock first**, then
  `WaitForRateLimit`, then the platform semaphore, then Send/Edit/Delete.

Failure mode: a worker that pulls a job for a rate-limited destination grabs that
destination's lock and **blocks in `WaitForRateLimit`/429-backoff while holding a
worker**. Other workers that pull the same destination's jobs block on that lock
too. A hot destination with a run of jobs at the channel front can park **all 30
workers** on its lock, and its backlog fills the shared 200 buffer. Result: the
pain is felt **globally** — free routes starve of workers, `Dispatch` (all sends)
blocks when the buffer fills, and clean-deletes drop. This is the
head-of-line/worker-starvation problem the interim PR #182 does not solve (it
serializes per-channel but still runs on the shared channel + workers).

## Decisions (agreed)

| # | Decision |
|---|----------|
| D1 | **Supersede #182.** One redesign PR carries the clean-delete fix; #182 closes. #182's building blocks are reused: `doWithRetry`/`doPostWithRetry` (429 Retry-After backoff), `Job.DeleteSentID`, the `MessageTracker` clean-delete hook, and the removal of `Delete`'s self-`Wait`. |
| D2 | **Per-destination lanes, spawn-on-demand + idle-reap.** A lane = a bounded buffered channel + one drainer goroutine, keyed by `job.Target`. Created on first job for a target; the drainer exits after an idle timeout with an empty lane and is re-created on the next job. Bounds goroutines/memory to *active* destinations. |
| D3 | **Global per-platform concurrency cap retained.** Each drainer acquires the platform semaphore (`discordSem`/`webhookSem`/`telegramSem`, existing sizes) around the actual API call, so N active lanes never fire N simultaneous requests (protects the global 50/sec bucket + socket limits). Isolation from lanes; global safety from the cap. |
| D4 | **`delivery_queue_size` becomes the PER-ROUTE buffer** (default stays 200 — now "200 queued for a single route", not shared). |
| D5 | **Overflow is per job kind.** Sends (`Dispatch`) block when a route's lane is full (per-route backpressure to the render pool — only that route's dispatch blocks, not all). Clean-deletes enqueue **non-blocking** (drop-on-full → re-cleaned on next startup load), so they never stall the tracker's eviction goroutine. |

## Design

### Lane

```
type lane struct {
    ch      chan *Job     // per-route buffer (delivery_queue_size)
    target  string
    // pending is queued + in-flight + about-to-be-enqueued jobs. Guarded by
    // FairQueue.lanesMu (incremented under it in enqueue; the reaper reads it
    // under it). Prevents reaping a lane that has work or an in-flight enqueue.
    pending int
}
```

### FairQueue changes

- Remove `ch` (shared), `destLocks`, the fixed `worker()` pool.
- Add `lanesMu sync.Mutex` + `lanes map[string]*lane`.
- Keep `discordSem`/`webhookSem`/`telegramSem` (global caps), `rateLimiter`,
  `failCounts`, `tracker`, `dispatcher` — the whole `processJob` pipeline is
  reused, just moved into the drainer and keyed off a lane instead of the shared
  channel.

### Enqueue (race-safe lane get-or-create)

```
func (fq *FairQueue) enqueue(job *Job, block bool) (accepted bool) {
    fq.lanesMu.Lock()
    if fq.stopped {                 // shutting down
        fq.lanesMu.Unlock()
        return false
    }
    l, ok := fq.lanes[job.Target]
    if !ok {
        l = &lane{ch: make(chan *Job, fq.perRouteBuf), target: job.Target}
        fq.lanes[job.Target] = l
        go fq.runLane(l)
    }
    l.pending++                     // reserve BEFORE releasing the lock
    fq.lanesMu.Unlock()

    if block {
        l.ch <- job                 // send: per-route backpressure
        return true
    }
    select {                        // clean-delete: non-blocking
    case l.ch <- job:
        return true
    default:
        fq.lanesMu.Lock(); l.pending--; fq.lanesMu.Unlock()
        return false                // dropped (re-cleaned on next load)
    }
}
```

`pending` is incremented **under `lanesMu`** before the lock is released, so the
reaper (which reads `pending` under `lanesMu`) can never reap a lane between an
enqueue's lock-release and its channel send. If the reaper deleted the lane just
before this enqueue took the lock, `fq.lanes[target]` is absent and a **new**
lane+drainer is created — no job is ever sent to a dead lane. This is the
standard "counter-under-lock" reap-safety pattern.

### Drainer

```
func (fq *FairQueue) runLane(l *lane) {
    defer fq.wg.Done()             // registered on spawn
    idle := time.NewTimer(fq.laneIdleTimeout)
    for {
        select {
        case job, ok := <-l.ch:
            if !ok { return }       // channel closed on shutdown
            if !idle.Stop() { <-idle.C }
            fq.processJob(job)      // full existing pipeline (below)
            fq.lanesMu.Lock(); l.pending--; fq.lanesMu.Unlock()
            idle.Reset(fq.laneIdleTimeout)
        case <-idle.C:
            fq.lanesMu.Lock()
            if l.pending == 0 {
                delete(fq.lanes, l.target)
                fq.lanesMu.Unlock()
                return              // reap: no work, no in-flight enqueue
            }
            fq.lanesMu.Unlock()
            idle.Reset(fq.laneIdleTimeout)
        }
    }
}
```

`processJob` keeps everything it does today **minus the destLock** (lane
serialization is inherent — one drainer per target): `WaitForRateLimit(target)`
→ acquire platform semaphore → alert-limit `Check` (Phase-2) → Send/Edit/Delete
(with `doWithRetry` 429 backoff) → tracking / snapshot write / reply-index /
failure-disable, OR the `DeleteSentID` clean-delete branch. `WaitForRateLimit`
stays before the semaphore so a backing-off lane doesn't hold a concurrency slot.

### Clean-delete integration (from #182, retargeted)

Unchanged from #182 except the hook enqueues to a **lane** instead of the shared
channel: `MessageTracker.cleanDelete` → `cleanDeleteHook` → `Dispatcher.enqueueCleanDelete`
→ `fq.enqueue(&Job{DeleteSentID:…}, block=false)`. A hot channel's delete burst
fills **its own** lane (200) and drains serially at its rate limit; excess drops
and is re-cleaned on the next startup load. It no longer competes with sends to
other channels for a shared buffer or shared workers.

### Dispatch / DispatchBypass

`Dispatcher.Dispatch(job)` → `fq.enqueue(job, block=true)`; `DispatchBypass`
likewise (bypass jobs skip the alert-limit `Check` inside `processJob`, as
today). Both route by `job.Target`.

### Shutdown

`Stop()` must drain all live lanes. Sequence:
1. Set `fq.stopped` under `lanesMu` (new enqueues rejected → return `false`;
   `Dispatch` callers already stopped upstream per the existing shutdown order).
2. Under `lanesMu`, `close(l.ch)` for every live lane; the drainers finish their
   buffered jobs and return on the closed channel.
3. `fq.wg.Wait()` (each drainer registers on spawn), then `fq.cancel()`.

This replaces "`close(ch)`; `wg.Wait()`" and preserves "queued jobs are still
delivered before shutdown". The tracker's clean-delete hook is guarded (D5 /
existing recover) so an eviction racing shutdown drops safely.

### Config

- `delivery_queue_size` (default 200) → **per-route** lane buffer (doc + example
  comment updated: "max buffered jobs per destination").
- `concurrent_discord_destinations` / `_webhooks` / `concurrent_telegram_destinations`
  → **global** per-platform concurrent-API-call caps (semaphores), unchanged.
- New (optional) `delivery_lane_idle_secs` (default ~60) — idle timeout before a
  lane's drainer is reaped.

## What carries over vs. changes

- **Reused as-is**: `doWithRetry` / `doPostWithRetry` (429 backoff), `Job.DeleteSentID`,
  `MessageTracker.cleanDelete` + `cleanDeleteHook`, `Delete` self-`Wait` removal,
  the whole per-job pipeline (rate-limit `Check`, tracking, snapshot, reply
  threading, edit-before-send, failure-disable), the platform semaphores, the
  `DiscordRateLimiter`.
- **Removed**: shared `ch`, `destLocks`, fixed `worker()` pool + `Start()`'s
  "spawn N workers".
- **Added**: `lane`, `lanes` map + `lanesMu`, `runLane` drainer, spawn/reap
  lifecycle, `enqueue`.

## Testing

- **Isolation** (the headline): a rate-limited/slow route (its sender's
  `WaitForRateLimit`/Send blocks) must NOT delay a free route — assert a job to
  a free target completes while a target-A job is parked. (Today this fails: the
  shared workers/buffer starve the free route.)
- **Per-route serialization**: N jobs to one target run one-at-a-time (max
  concurrency 1 for that target) while different targets run concurrently up to
  the platform semaphore.
- **Reap safety** (race): interleave rapid enqueue + idle-reap for one target
  under `-race`; no job lost, no send-on-closed panic, no double drainer. Assert
  a reaped-then-re-enqueued target still delivers.
- **Global cap**: with `concurrent_discord_destinations=2` and many active
  lanes, at most 2 concurrent Discord API calls.
- **Overflow**: a full lane blocks a send (backpressure) but drops a clean-delete
  (non-blocking); other lanes unaffected.
- **Shutdown**: buffered jobs across many lanes all deliver before `Stop()`
  returns; `-race` clean.
- **Clean-delete end-to-end**: eviction burst on one channel serializes on its
  lane and succeeds under a 429-then-OK sender (carried from #182).
- Existing delivery tests (send/edit/tracker/rate-limit/snapshot) stay green.

## Risks / edge cases

- **Reap↔enqueue race** — the core risk; addressed by the counter-under-lock
  pattern (D2) and covered by a `-race` test.
- **Lane-map lock contention** — `lanesMu` is taken on enqueue, per-job
  `pending--`, and reap. Critical sections are tiny (map op + int). If profiling
  ever shows contention, shard the map by target hash. Not expected at current
  scale.
- **Goroutine count** — bounded by *active* destinations (idle-reaped). A pathological
  fan-out (thousands simultaneously active) = thousands of mostly-idle drainers;
  acceptable (cheap goroutines) and self-trimming.
- **Ordering across a reap** — a target reaped then re-created starts a fresh
  lane; since reap only happens with an empty lane + no pending, no reordering of
  live jobs occurs.

## Out of scope / deferred

- Priority between sends and clean-deletes within a lane (FIFO for now).
- Cross-platform fairness knobs beyond the existing per-platform caps.
- Persisting per-lane depth metrics (add a gauge if useful, not required).
