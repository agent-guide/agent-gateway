package configmgr

import "context"

type MutationAction string

const (
	MutationCreate  MutationAction = "create"
	MutationUpdate  MutationAction = "update"
	MutationDelete  MutationAction = "delete"
	MutationRefresh MutationAction = "refresh"
)

// MutationGuard composes cross-resource checks with a manager's existing local
// mutation. Install during construction; apply must be called synchronously at
// most once, without reentering the same manager's mutation methods.
type MutationGuard[T any] func(context.Context, MutationAction, string, *T, func(context.Context) error) error

func SelectMutationGuard[T any](guards []MutationGuard[T]) MutationGuard[T] {
	if len(guards) > 1 {
		panic("only one mutation guard may be installed")
	}
	if len(guards) == 1 {
		return guards[0]
	}
	return nil
}

func GuardMutation[T any](guard MutationGuard[T], ctx context.Context, action MutationAction, id string, next *T, apply func(context.Context) error) error {
	if guard != nil {
		return guard(ctx, action, id, next, apply)
	}
	return apply(ctx)
}
