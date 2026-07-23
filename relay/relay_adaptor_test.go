package relay

import (
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	taskaistarslab "github.com/QuantumNous/new-api/relay/channel/task/aistarslab"
	tasksora "github.com/QuantumNous/new-api/relay/channel/task/sora"
	"github.com/stretchr/testify/require"
)

func TestGetTaskAdaptorForChannelRoutesOnlyAistarsLabOpenAIBaseURL(t *testing.T) {
	platform := constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeOpenAI))

	providerAdaptor := GetTaskAdaptorForChannel(platform, "https://api.video.aistarslab.com/openai")
	require.IsType(t, &taskaistarslab.TaskAdaptor{}, providerAdaptor)

	openAIAdaptor := GetTaskAdaptorForChannel(platform, "https://api.openai.com")
	require.IsType(t, &tasksora.TaskAdaptor{}, openAIAdaptor)
}
