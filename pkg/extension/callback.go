package extension

import "context"

// Module panic values and arbitrary callback diagnostics never cross the public
// boundary, including resource teardown running outside an HTTP handler.
func callback[T any](fn func() (T, error)) (value T, err error) {
	defer func() {
		if recover() != nil {
			err = failure("callback_failed", "", "")
		}
	}()
	return fn()
}
func closeResource(ctx context.Context, r Resource) error {
	_, err := callback(func() (struct{}, error) { return struct{}{}, r.Close(ctx) })
	return err
}
func healthResource(ctx context.Context, r Resource) error {
	_, err := callback(func() (struct{}, error) { return struct{}{}, r.Health(ctx) })
	return err
}
