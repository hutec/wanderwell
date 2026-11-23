package strava

import (
	"context"
	"errors"
	"net/http"
)

const stravaRequestMaxAttempts = 3

// ExecuteStravaRequest queues a request through s and acquires fresh permission
// from the shared gate for each attempt, using the worker context. Only HTTP 429
// responses are retried, with at most three attempts total, inside the same
// worker callback (never resubmitted). Gate quota/cooldown waits occupy a worker
// and are interrupted by caller cancellation or scheduler shutdown.
// ExecuteRequest's lifecycle and callback restrictions apply.
//
// Every acquired permission is observed exactly once, using response headers
// even when request returns an error. Nil responses and cancellation before
// sending finalize permission without refunding it. Observation errors are
// joined with the request error without discarding its result. The final attempt's
// result and callback error are preserved; a final 429 without a callback error
// returns a rate-limit error instead of success.
//
// The callback must use its context and consume/close response bodies before
// returning (as the generated Strava client does). This helper does not close
// bodies or support streaming results.
func ExecuteStravaRequest[T any](ctx context.Context, s *RequestScheduler, gate *RateLimitGate, kind RequestKind, request func(context.Context) (T, *http.Response, error)) (T, error) {
	return ExecuteRequest(ctx, s, func(ctx context.Context) (value T, err error) {
		for range stravaRequestMaxAttempts {
			receipt, acquireErr := gate.Acquire(ctx, kind)
			if acquireErr != nil {
				return value, acquireErr
			}
			var response *http.Response
			if err = ctx.Err(); err == nil {
				value, response, err = request(ctx)
			}
			rateLimited := response != nil && response.StatusCode == http.StatusTooManyRequests
			if rateLimited && err == nil {
				err = errors.New("strava rate limit exceeded (HTTP 429)")
			}
			// Finalize even unused permissions, and publish headers + cooldown
			// atomically before retrying or returning, including the last 429.
			if observeErr := gate.Observe(receipt, response); observeErr != nil {
				return value, errors.Join(err, observeErr)
			}
			if !rateLimited {
				return value, err
			}
		}
		return value, err
	})
}
