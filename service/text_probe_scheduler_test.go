package service

import (
	"context"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

func TestTextProbeRoundsContinueWhileImageTaskRuns(t *testing.T) {
	truncate(t)
	imageStarted := make(chan struct{})
	releaseImage := make(chan struct{})
	imageDone := make(chan error, 1)
	textDone := make(chan error, 2)
	image := &stubScheduledHandler{taskType: model.SystemTaskTypeImageChannelHealthProbe, enabled: true, interval: time.Second, onRun: func(_ context.Context, task *model.SystemTask, runner string) {
		close(imageStarted)
		<-releaseImage
		imageDone <- model.FinishSystemTask(task.TaskID, runner, model.SystemTaskStatusSucceeded, nil, "")
	}}
	text := &stubScheduledHandler{taskType: model.SystemTaskTypeChannelHealthProbe, enabled: true, interval: time.Second, onRun: func(_ context.Context, task *model.SystemTask, runner string) {
		textDone <- model.FinishSystemTask(task.TaskID, runner, model.SystemTaskStatusSucceeded, nil, "")
	}}
	withSystemTaskRegistry(t, image, text)
	defer func() {
		close(releaseImage)
		select {
		case err := <-imageDone:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Error("image task did not finish")
		}
	}()
	runSystemTaskScheduler()
	runSystemTaskClaimPass("probe-isolation")
	select {
	case <-imageStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("image task did not start")
	}
	for round := 0; round < 2; round++ {
		select {
		case err := <-textDone:
			require.NoError(t, err)
		case <-time.After(3 * time.Second):
			t.Fatal("image task blocked a text round")
		}
		if round == 0 {
			latest, err := model.GetLatestSystemTask(text.taskType)
			require.NoError(t, err)
			require.NoError(t, model.DB.Model(&model.SystemTask{}).Where("task_id = ?", latest.TaskID).Update("updated_at", common.GetTimestamp()-5).Error)
			runSystemTaskScheduler()
			runSystemTaskClaimPass("probe-isolation")
		}
	}
	require.Equal(t, int64(2), countSystemTasks(t, text.taskType))
	require.Equal(t, int64(1), countSystemTasks(t, image.taskType))
}
