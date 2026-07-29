package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

// RegisterScheduledSystemTasks wires the periodic channel test, upstream model
// update, and async task polling (Midjourney / Suno / video) jobs into the
// system task framework so a DB lease dedups execution across multiple master
// instances and each run is recorded as one task row. Call this before
// service.StartSystemTaskRunner.
func RegisterScheduledSystemTasks() {
	service.RegisterSystemTaskHandler(channelTestHandler{})
	service.RegisterSystemTaskHandler(channelHealthProbeHandler{})
	service.RegisterSystemTaskHandler(modelUpdateHandler{})
	service.RegisterSystemTaskHandler(midjourneyPollHandler{})
	service.RegisterSystemTaskHandler(asyncTaskPollHandler{})
}

type channelHealthProbeHandler struct{}

const (
	channelHealthProbeTimeout     = 15 * time.Second
	channelHealthProbeConcurrency = 4
	channelHealthProbeRunLimit    = 20
)

type channelHealthProbeOutcome struct {
	tested    bool
	succeeded bool
}

func channelHealthProbeTimeoutForPath(requestPath string) time.Duration {
	if requestPath == "/v1/images/generations" || requestPath == "/v1/images/edits" {
		return time.Minute
	}
	return channelHealthProbeTimeout
}

func (channelHealthProbeHandler) Type() string { return model.SystemTaskTypeChannelHealthProbe }

func (channelHealthProbeHandler) Enabled() bool { return service.HasDueChannelHealthProbe() }

func (channelHealthProbeHandler) Interval() time.Duration { return 15 * time.Second }

func (channelHealthProbeHandler) NewPayload() any { return nil }

func channelHealthProbeEndpointType(requestPath string) string {
	switch requestPath {
	case "/v1/responses":
		return string(constant.EndpointTypeOpenAIResponse)
	case "/v1/responses/compact":
		return string(constant.EndpointTypeOpenAIResponseCompact)
	case "/v1/messages":
		return string(constant.EndpointTypeAnthropic)
	case "/v1/images/generations", "/v1/images/edits":
		return string(constant.EndpointTypeImageGeneration)
	case "/v1/embeddings":
		return string(constant.EndpointTypeEmbeddings)
	case "/v1/rerank", "/rerank":
		return string(constant.EndpointTypeJinaRerank)
	default:
		return string(constant.EndpointTypeOpenAI)
	}
}

func (channelHealthProbeHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	var claimCount atomic.Int32
	results := make(chan channelHealthProbeOutcome, channelHealthProbeConcurrency)
	var workers sync.WaitGroup
	for range channelHealthProbeConcurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil && claimCount.Add(1) <= channelHealthProbeRunLimit {
				targets := service.ClaimDueChannelHealthProbes(1)
				if len(targets) == 0 {
					return
				}
				results <- runChannelHealthProbe(ctx, testUserID, targets[0])
			}
		}()
	}
	go func() {
		workers.Wait()
		close(results)
	}()

	summary := channelTestSummary{}
	for result := range results {
		if !result.tested {
			continue
		}
		summary.Tested++
		if result.succeeded {
			summary.Succeeded++
		} else {
			summary.Failed++
		}
	}
	if ctx.Err() != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, summary, ctx.Err())
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

func runChannelHealthProbe(ctx context.Context, testUserID int, target service.ChannelHealthProbeTarget) channelHealthProbeOutcome {
	if ctx.Err() != nil {
		service.ReleaseChannelHealthProbe(target)
		return channelHealthProbeOutcome{}
	}
	channel, err := model.CacheGetChannel(target.ChannelID)
	if err != nil || channel == nil {
		if err == nil {
			err = fmt.Errorf("channel #%d not found", target.ChannelID)
		}
		service.CompleteChannelHealthProbe(target, service.ChannelHealthProbeResult{
			Class:  service.ChannelFailureTransient,
			Reason: err.Error(),
		})
		return channelHealthProbeOutcome{tested: true}
	}
	if channel.Status != common.ChannelStatusEnabled {
		service.SuspendChannelHealth(channel)
		return channelHealthProbeOutcome{}
	}

	probeCtx, cancelProbe := context.WithTimeout(ctx, channelHealthProbeTimeoutForPath(target.RequestPath))
	result := testChannel(probeCtx, channel, testUserID, target.ModelName, channelHealthProbeEndpointType(target.RequestPath), false)
	probeTimedOut := errors.Is(probeCtx.Err(), context.DeadlineExceeded)
	cancelProbe()
	if ctx.Err() != nil {
		service.ReleaseChannelHealthProbe(target)
		return channelHealthProbeOutcome{}
	}
	if result.localErr != nil && result.newAPIError == nil {
		if probeTimedOut {
			service.CompleteChannelHealthProbe(target, service.ChannelHealthProbeResult{
				Class:      service.ChannelFailureUncertain,
				Reason:     "active_probe_timeout",
				StatusCode: http.StatusGatewayTimeout,
			})
		} else {
			service.ReleaseChannelHealthProbe(target)
		}
		return channelHealthProbeOutcome{tested: true}
	}

	probeResult := service.ChannelHealthProbeResult{Success: result.localErr == nil && result.newAPIError == nil}
	if !probeResult.Success {
		probeResult.Class = service.ChannelFailureTransient
		if result.localErr != nil {
			probeResult.Reason = result.localErr.Error()
		}
		if result.newAPIError != nil {
			decision := service.DecideChannelFailureForModel(result.context, result.newAPIError, target.ModelName, 1, true, false)
			probeResult.Class = decision.Class
			probeResult.Reason = decision.Reason
			probeResult.StatusCode = result.newAPIError.StatusCode
		}
	}
	service.CompleteChannelHealthProbe(target, probeResult)
	return channelHealthProbeOutcome{tested: true, succeeded: probeResult.Success}
}

// channelTestHandler runs the scheduled "test all channels" job. Enablement and
// cadence still come from the monitor settings; only the execution path moved
// into the system task runner.
type channelTestHandler struct{}

func (channelTestHandler) Type() string { return model.SystemTaskTypeChannelTest }

func (channelTestHandler) Enabled() bool {
	return operation_setting.GetMonitorSetting().AutoTestChannelEnabled
}

func (channelTestHandler) Interval() time.Duration {
	minutes := operation_setting.GetMonitorSetting().AutoTestChannelMinutes
	if minutes <= 0 {
		minutes = 10
	}
	return time.Duration(minutes * float64(time.Minute))
}

func (channelTestHandler) NewPayload() any { return nil }

// channelTestTaskPayload controls one channel_test run. A nil/empty payload is a
// scheduled run, which uses the configured monitor ChannelTestMode and does not
// notify. A manual "test all channels" trigger sets Mode=scheduled_all and
// Notify=true to reproduce the legacy manual behavior (test every channel and
// notify root on completion).
type channelTestTaskPayload struct {
	Mode   string `json:"mode,omitempty"`
	Notify bool   `json:"notify,omitempty"`
}

func (channelTestHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := channelTestTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	summary, err := runChannelTestTask(ctx, payload.Mode, payload.Notify, service.NewSystemTaskProgressReporter(task, runnerID))
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// modelUpdateHandler runs the scheduled upstream model update detection job.
type modelUpdateHandler struct{}

func (modelUpdateHandler) Type() string { return model.SystemTaskTypeModelUpdate }

func (modelUpdateHandler) Enabled() bool {
	return common.GetEnvOrDefaultBool("CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_ENABLED", true)
}

func (modelUpdateHandler) Interval() time.Duration {
	intervalMinutes := common.GetEnvOrDefault(
		"CHANNEL_UPSTREAM_MODEL_UPDATE_TASK_INTERVAL_MINUTES",
		channelUpstreamModelUpdateTaskDefaultIntervalMinutes,
	)
	if intervalMinutes < 1 {
		intervalMinutes = channelUpstreamModelUpdateTaskDefaultIntervalMinutes
	}
	return time.Duration(intervalMinutes) * time.Minute
}

func (modelUpdateHandler) NewPayload() any { return nil }

// modelUpdateTaskPayload controls one model_update run. A scheduled run
// (Manual=false) respects the per-channel minimum check interval and may
// auto-apply detected models when a channel has auto-sync enabled. A manual
// "detect all" trigger sets Manual=true to reproduce the legacy detect-all
// semantics: force a re-check regardless of the interval and never auto-apply,
// so the admin reviews and applies changes explicitly.
type modelUpdateTaskPayload struct {
	Manual bool `json:"manual,omitempty"`
}

func (modelUpdateHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := modelUpdateTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	summary := runChannelUpstreamModelUpdateTaskOnce(ctx, payload.Manual, !payload.Manual, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// midjourneyPollHandler runs one Midjourney polling pass per scheduled run.
// Enabled() folds the "are there unfinished tasks?" check into enablement so the
// scheduler creates no row when the system is idle; only when at least one
// Midjourney task is in progress does a row get scheduled.
type midjourneyPollHandler struct{}

func (midjourneyPollHandler) Type() string { return model.SystemTaskTypeMidjourneyPoll }

func (midjourneyPollHandler) Enabled() bool {
	return constant.UpdateTask && model.HasUnfinishedMidjourneyTasks()
}

func (midjourneyPollHandler) Interval() time.Duration { return 15 * time.Second }

func (midjourneyPollHandler) NewPayload() any { return nil }

func (midjourneyPollHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary := runMidjourneyTaskUpdateOnce(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

// asyncTaskPollHandler runs one async-task (Suno/video) polling pass per
// scheduled run. Like midjourneyPollHandler, Enabled() folds in the unfinished
// task existence check so an idle system schedules no rows.
type asyncTaskPollHandler struct{}

func (asyncTaskPollHandler) Type() string { return model.SystemTaskTypeAsyncTaskPoll }

func (asyncTaskPollHandler) Enabled() bool {
	return constant.UpdateTask && model.HasUnfinishedSyncTasks()
}

func (asyncTaskPollHandler) Interval() time.Duration { return 15 * time.Second }

func (asyncTaskPollHandler) NewPayload() any { return nil }

func (asyncTaskPollHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	summary := service.RunTaskPollingOnce(ctx, service.NewSystemTaskProgressReporter(task, runnerID))
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, summary, nil)
}

func finishSystemTaskHandler(task *model.SystemTask, runnerID string, status model.SystemTaskStatus, result any, runErr error) {
	errorMessage := ""
	if runErr != nil {
		errorMessage = runErr.Error()
	}
	if err := model.FinishSystemTask(task.TaskID, runnerID, status, result, errorMessage); err != nil {
		common.SysLog(fmt.Sprintf("system task %s failed to persist result: %v", task.TaskID, err))
	}
}
