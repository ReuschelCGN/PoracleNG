# Mixed collations during migration

Legacy Knex databases skip migration `000001`, including its database-default
collation setting. Older tables can use `utf8mb4_unicode_ci` (or another
collation, or `utf8mb3`) while tables created later inherit a different
default, such as MySQL 8's `utf8mb4_0900_ai_ci`. Changing a database default
does not change existing columns.

The original migration `000009` (fold the accidental profile 0 into profile 1)
compared user IDs across tables directly, so on such databases it failed with
`Illegal mix of collations for operation 'UNION'`. This does not involve
foreign keys; user deletion remains application-managed.

## The fix

- `000009` is now a no-op. Fixing it in place would not reach the databases
  that needed it most: older builds forced a dirty migration clean and moved
  on, so those databases already record `000009` as applied.
- `000010_profile_zero_to_one_retry` is the corrected repair, and every
  database runs it. It normalises IDs to `utf8mb4_unicode_ci` in temporary
  tables and converts only the persistent table's side of each comparison, so
  every lookup uses a temporary table's primary key. It does not alter
  persistent columns. Where `000009` already succeeded it changes nothing.
- All of `000010`'s data changes commit in one transaction, so a failure
  part-way leaves the data unchanged (InnoDB tables; a MyISAM table would not
  roll back).
- Migrations `000002`–`000004` now create their tables with
  `utf8mb4_unicode_ci` instead of inheriting an installation-dependent default.

## Dirty migrations

Startup no longer forces a dirty migration clean and advances past it: a
failed multi-statement migration may be only partly applied. A dirty `000009`
or `000010` is retried automatically, as both are safe to re-run. Any other
dirty version stops startup with an error.

To recover from another dirty version, stop the application, take a backup,
read the original error and inspect `schema_migrations` (`version`, `dirty`).
Either restore a backup from before that migration, or finish/undo its
statements by hand and then set `schema_migrations` to the version you have
actually reached with `dirty = 0`.

## Regression tests

The tests in `processor/internal/db/migrate_collation_test.go` need
`PORACLENG_MIGRATION_TEST_DSN` pointing at MySQL 8 or MariaDB, with permission
to create and drop databases. Each creates and removes its own uniquely named
database. They cover mixed collations across all tracking tables, re-running
the repair, rollback after a failure part-way, and dirty-state handling.
