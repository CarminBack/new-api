package controller

import (
	"context"
	"errors"
	"fmt"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type channelHealthProbeHandler struct{ images bool }

const (
	channelHealthProbeTimeout     = 15 * time.Second
	imageHealthProbeTimeout       = 5 * time.Minute
	channelHealthProbeConcurrency = 4
	channelHealthProbeRunLimit    = 20
	imageHealthProbeConcurrency   = 3
	imageHealthProbeRunLimit      = 6
)

type channelHealthProbeOutcome struct {
	tested    bool
	succeeded bool
}

func channelHealthProbeTimeoutForPath(requestPath string) time.Duration {
	if service.IsImageGenerationPath(requestPath) {
		return imageHealthProbeTimeout
	}
	return channelHealthProbeTimeout
}

func (h channelHealthProbeHandler) Type() string {
	if h.images {
		return model.SystemTaskTypeImageChannelHealthProbe
	}
	return model.SystemTaskTypeChannelHealthProbe
}

func (h channelHealthProbeHandler) Enabled() bool {
	return service.HasDueChannelHealthProbeForImages(h.images)
}

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
	case "/v1/embeddings":
		return string(constant.EndpointTypeEmbeddings)
	case "/v1/images/generations", "/v1/images/edits", "/v1/images/variations":
		return string(constant.EndpointTypeImageGeneration)
	case "/v1/rerank", "/rerank":
		return string(constant.EndpointTypeJinaRerank)
	default:
		return string(constant.EndpointTypeOpenAI)
	}
}

func startChannelHealthProbeWorkers(
	ctx context.Context,
	testUserID int,
	concurrency int,
	runLimit int,
	claim func(int) []service.ChannelHealthProbeTarget,
	results chan<- channelHealthProbeOutcome,
	workers *sync.WaitGroup,
) {
	var claimCount atomic.Int32
	for range concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for ctx.Err() == nil && claimCount.Add(1) <= int32(runLimit) {
				targets := claim(1)
				if len(targets) == 0 {
					return
				}
				results <- runChannelHealthProbe(ctx, testUserID, targets[0])
			}
		}()
	}
}

func (h channelHealthProbeHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	results := make(chan channelHealthProbeOutcome, channelHealthProbeConcurrency+imageHealthProbeConcurrency)
	var workers sync.WaitGroup
	if h.images {
		startChannelHealthProbeWorkers(
			ctx, testUserID, imageHealthProbeConcurrency, imageHealthProbeRunLimit,
			service.ClaimDueImageChannelHealthProbes, results, &workers,
		)
	} else {
		startChannelHealthProbeWorkers(
			ctx, testUserID, channelHealthProbeConcurrency, channelHealthProbeRunLimit,
			service.ClaimDueStandardChannelHealthProbes, results, &workers,
		)
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
		} else if service.IsImageGenerationPath(target.RequestPath) {
			service.CompleteChannelHealthProbe(target, service.ChannelHealthProbeResult{
				Class:  service.ChannelFailureUncertain,
				Reason: result.localErr.Error(),
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
	if probeTimedOut {
		probeResult = service.ChannelHealthProbeResult{Class: service.ChannelFailureUncertain, Reason: "active_probe_timeout", StatusCode: http.StatusGatewayTimeout}
	}
	service.CompleteChannelHealthProbe(target, probeResult)
	return channelHealthProbeOutcome{tested: true, succeeded: probeResult.Success}
}
