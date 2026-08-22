package operation_setting

import (
	"os"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

type MonitorSetting struct {
	AutoTestChannelEnabled bool              `json:"auto_test_channel_enabled"`
	AutoTestChannelMinutes float64           `json:"auto_test_channel_minutes"`
	ChannelTestMode        string            `json:"channel_test_mode"`
	ProbeModels            map[string]string `json:"probe_models"`
}

const (
	ChannelTestModeScheduledAll    = "scheduled_all"
	ChannelTestModePassiveRecovery = "passive_recovery"
)

// 默认配置
var monitorSetting = MonitorSetting{
	AutoTestChannelEnabled: true,
	AutoTestChannelMinutes: 5,
	ChannelTestMode:        ChannelTestModeScheduledAll,
	ProbeModels:            map[string]string{},
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("monitor_setting", &monitorSetting)
}

func GetMonitorSetting() *MonitorSetting {
	if os.Getenv("CHANNEL_TEST_FREQUENCY") != "" {
		frequency, err := strconv.Atoi(os.Getenv("CHANNEL_TEST_FREQUENCY"))
		if err == nil && frequency > 0 {
			monitorSetting.AutoTestChannelEnabled = true
			monitorSetting.AutoTestChannelMinutes = float64(frequency)
			monitorSetting.ChannelTestMode = ChannelTestModeScheduledAll
		}
	}
	if enabled, ok := os.LookupEnv("CHANNEL_TEST_ENABLED"); ok {
		parsed, err := strconv.ParseBool(enabled)
		if err == nil {
			monitorSetting.AutoTestChannelEnabled = parsed
		}
	}
	if monitorSetting.ChannelTestMode != ChannelTestModePassiveRecovery {
		monitorSetting.ChannelTestMode = ChannelTestModeScheduledAll
	}
	return &monitorSetting
}

// GetProbeModel returns the explicitly configured model for a channel group.
// An empty result preserves the channel's existing TestModel/model fallback.
func GetProbeModel(group string) string {
	modelName := strings.TrimSpace(GetMonitorSetting().ProbeModels[group])
	return modelName
}
