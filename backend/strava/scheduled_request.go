package strava

import (
	"context"
	"errors"
	"net/http"
)

// ExecuteStravaRequest queues one request through s and acquires permission from
// the shared gate using the worker context. Gate waits occupy a worker and are
// interrupted by caller cancellation or scheduler shutdown. ExecuteRequest's
// lifecycle and callback restrictions apply; requests are never retried.
//
// Every acquired permission is observed exactly once, using response headers
// even when request returns an error. Nil responses and cancellation before
// sending finalize permission without refunding it. Observation errors are
// joined with the request error without discarding its result.
//
// The callback must use its context and consume/close response bodies before
// returning (as the generated Strava client does). This helper does not close
// bodies or support streaming results.
func ExecuteStravaRequest[T any](ctx context.Context, s *RequestScheduler, gate *RateLimitGate, kind RequestKind, request func(context.Context) (T, *http.Response, error)) (T, error) {
	return ExecuteRequest(ctx, s, func(ctx context.Context) (value T, err error) {
		receipt, err := gate.Acquire(ctx, kind)
		if err != nil {
			return value, err
		}
		var headers http.Header
		defer func() {
			if observeErr := gate.Observe(receipt, headers); observeErr != nil {
				err = errors.Join(err, observeErr)
			}
		}()
		if err = ctx.Err(); err != nil {
			return value, err
		}
		value, response, err := request(ctx)
		if response != nil {
			headers = response.Header
		}
		return value, err
	})
}
