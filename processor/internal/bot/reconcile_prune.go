package bot

import (
	"errors"

	"github.com/pokemon/poracleng/processor/internal/config"
	"github.com/pokemon/poracleng/processor/internal/geofence"
	"github.com/pokemon/poracleng/processor/internal/store"
)

// PruneOptions selects which prunes PruneForMembership runs.
type PruneOptions struct {
	// Overrides prunes per-rule override_areas. Low-value derived data whose
	// loss is visible and self-correcting, so callers run it unconditionally.
	Overrides bool
	// UserAreas prunes humans.area and every profile. Irreversible, so it is
	// gated behind [reconciliation.*] prune_user_areas (default false).
	UserAreas bool
}

// PruneForMembership removes, after a community-membership change, every
// area the human may no longer hold, judged by PermittedAreas so the prune
// can never undo a grant. human must carry the NEW membership and
// area_restriction.
//
// trusted is passed as true: prunes act only on the community dimension, so
// a non-userSelectable fence granted via trusted survives. No fences => skip
// entirely: an empty list means the load failed, not that nothing is allowed.
func PruneForMembership(hs store.HumanStore, fences []geofence.Fence, cfg *config.Config,
	human *store.Human, admin bool, opts PruneOptions) ([]string, error) {
	if len(fences) == 0 || human == nil {
		return nil, nil
	}
	permitted := PermittedAreaKeys(PermittedAreas(fences, cfg, human, admin, true))

	var errs []error
	if opts.Overrides {
		if err := hs.PruneOverrideAreas(human.ID, permitted); err != nil {
			errs = append(errs, err)
		}
	}
	var removed []string
	if opts.UserAreas {
		r, err := hs.PruneUserAreas(human.ID, permitted)
		if err != nil {
			errs = append(errs, err)
		}
		removed = r
	}
	return removed, errors.Join(errs...)
}
