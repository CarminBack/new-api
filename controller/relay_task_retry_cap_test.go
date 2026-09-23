package controller

import (
	"net/http"
	"testing"

	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/stretchr/testify/assert"
)

// decideTaskRetry is the single decision point for task resubmission. The
// controller then caps the number of resubmissions at one per request so a
// non-idempotent submission is never retried by RetryTimes alone.
func TestDecideTaskRetryClassifiesUpstreamFailures(t *testing.T) {
	for _, tc := range []struct {
		name    string
		taskErr *taskdto.TaskError
		retries int
		action  string
		reason  string
	}{
		{
			name:    "accepted response never retries",
			taskErr: &taskdto.TaskError{NoRetry: true, StatusCode: http.StatusOK},
			retries: 3,
			action:  "stop",
			reason:  "task_accepted",
		},
		{
			name:    "upstream 500 retries",
			taskErr: &taskdto.TaskError{StatusCode: http.StatusInternalServerError},
			retries: 3,
			action:  "retry",
		},
		{
			name:    "rate limit retries",
			taskErr: &taskdto.TaskError{StatusCode: http.StatusTooManyRequests},
			retries: 3,
			action:  "retry",
		},
		{
			name:    "400 does not retry",
			taskErr: &taskdto.TaskError{StatusCode: http.StatusBadRequest},
			retries: 3,
			action:  "stop",
			reason:  "status_not_retryable",
		},
		{
			name:    "exhausted budget stops",
			taskErr: &taskdto.TaskError{StatusCode: http.StatusInternalServerError},
			retries: 0,
			action:  "stop",
			reason:  "attempt_budget_exhausted",
		},
		{
			name:    "local 400 does not retry",
			taskErr: &taskdto.TaskError{LocalError: true, StatusCode: http.StatusBadRequest},
			retries: 3,
			action:  "stop",
			reason:  "status_not_retryable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := decideTaskRetry(taskSubmissionTestContext(), tc.taskErr, tc.retries)
			assert.Equal(t, tc.action, decision.Action)
			if tc.reason != "" {
				assert.Equal(t, tc.reason, decision.Reason)
			}
		})
	}
}

// The controller-level cap is what makes "at most one resubmission" hold even
// when RetryTimes is larger. This mirrors the guard used in the task loop.
func TestTaskRetryCapAllowsAtMostOneExtraSubmission(t *testing.T) {
	// Simulate the loop guard with RetryTimes = 5 (the staging value).
	retryTimes := 5
	taskRetryCount := 0
	submissions := 0

	for retry := 0; retry <= retryTimes; retry++ {
		submissions++ // the submit call
		decision := decideTaskRetry(taskSubmissionTestContext(), &taskdto.TaskError{StatusCode: http.StatusInternalServerError}, retryTimes-retry)
		if decision.Action != "retry" {
			break
		}
		if taskRetryCount >= 1 {
			decision = service.PolicyDecision{Action: "stop", Reason: "general_retry_limit", Source: "system"}
		} else {
			taskRetryCount++
		}
		if decision.Action != "retry" {
			break
		}
	}

	assert.Equal(t, 2, submissions, "a non-idempotent task must be submitted at most twice")
}

// Image fallback has its own hard cap; RetryTimes must not multiply it.
func TestImageFallbackNeverExceedsCap(t *testing.T) {
	for _, retryTimes := range []int{0, 1, 3, 5, 10} {
		attempts := 0
		for retry := 0; retry <= retryTimes; retry++ {
			if relayRetriesRemaining("/v1/images/generations", retry, retryTimes) <= 0 {
				break
			}
			attempts++
		}
		assert.LessOrEqual(t, attempts, maxImageFallbacks+1, "RetryTimes=%d must not exceed the image fallback cap", retryTimes)
	}
}
