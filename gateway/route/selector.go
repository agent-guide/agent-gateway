package route

import (
	"fmt"
	"math/rand"

	"github.com/agent-guide/caddy-agent-gateway/internal/statuserr"
)

// RouteSelector chooses an eligible route target for a request.
type RouteTargetSelector interface {
	SelectTarget(route Route, req ResolveRequest) (*RouteTarget, error)
}

// DefaultRouteSelector implements the current built-in weighted/failover target selection.
type DefaultRouteSelector struct{}

func (s DefaultRouteSelector) SelectTarget(r Route, req ResolveRequest) (*RouteTarget, error) {
	candidates := make([]RouteTarget, 0, len(r.Targets))
	for _, target := range r.Targets {
		if target.Disabled || target.ProviderRef == "" {
			continue
		}
		if !matchesConditions(target.Conditions, req) {
			continue
		}
		candidates = append(candidates, target)
	}
	if len(candidates) == 0 {
		return nil, statuserr.New(502, fmt.Sprintf("route %q has no eligible targets", r.ID))
	}

	var (
		failover    []RouteTarget
		weighted    []RouteTarget
		conditional []RouteTarget
		other       []RouteTarget
	)
	for _, target := range candidates {
		switch target.Mode {
		case TargetModeFailover:
			failover = append(failover, target)
		case TargetModeConditional:
			conditional = append(conditional, target)
		case TargetModeWeighted, "":
			weighted = append(weighted, target)
		default:
			other = append(other, target)
		}
	}

	r.Policy.Defaults()
	for _, group := range selectionOrder(r.Policy.Selection.Strategy, r.Policy.Fallback.Enabled, weighted, conditional, failover, other) {
		if len(group) == 0 {
			continue
		}
		switch primaryMode(group[0]) {
		case TargetModeFailover:
			best := group[0]
			for _, item := range group[1:] {
				if item.Priority < best.Priority {
					best = item
				}
			}
			return &best, nil
		default:
			chosen := chooseWeighted(group)
			return &chosen, nil
		}
	}

	return nil, statuserr.New(502, fmt.Sprintf("route %q has no eligible targets", r.ID))

}

func selectionOrder(strategy RouteSelectionStrategy, allowFallback bool, weighted, conditional, failover, other []RouteTarget) [][]RouteTarget {
	switch strategy {
	case RouteSelectionStrategyWeighted:
		if allowFallback {
			return [][]RouteTarget{weighted, conditional, failover, other}
		}
		return [][]RouteTarget{weighted}
	case RouteSelectionStrategyConditional:
		if allowFallback {
			return [][]RouteTarget{conditional, weighted, failover, other}
		}
		return [][]RouteTarget{conditional}
	case RouteSelectionStrategyFailover:
		if allowFallback {
			return [][]RouteTarget{failover, weighted, conditional, other}
		}
		return [][]RouteTarget{failover}
	case RouteSelectionStrategyAuto, "":
		// Try conditional targets first (they carry explicit eligibility criteria),
		// then fall through to weighted targets, then failover.
		if allowFallback {
			return [][]RouteTarget{conditional, weighted, failover, other}
		}
		return [][]RouteTarget{conditional, weighted}
	default:
		if allowFallback {
			return [][]RouteTarget{conditional, weighted, failover, other}
		}
		return [][]RouteTarget{conditional, weighted}
	}
}

func primaryMode(target RouteTarget) TargetMode {
	if target.Mode == "" {
		return TargetModeWeighted
	}
	return target.Mode
}

func chooseWeighted(targets []RouteTarget) RouteTarget {
	if len(targets) == 1 {
		return targets[0]
	}

	total := 0
	for _, target := range targets {
		weight := target.Weight
		if weight <= 0 {
			weight = 1
		}
		total += weight
	}
	if total <= 0 {
		return targets[0]
	}

	pick := rand.Intn(total)
	for _, target := range targets {
		weight := target.Weight
		if weight <= 0 {
			weight = 1
		}
		if pick < weight {
			return target
		}
		pick -= weight
	}
	return targets[0]
}
