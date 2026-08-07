package model

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

type ChannelHealthState struct {
	ScopeKey               string `json:"scope_key" gorm:"type:varchar(191);primaryKey"`
	ChannelID              int    `json:"channel_id" gorm:"index"`
	Fingerprint            string `json:"fingerprint" gorm:"type:varchar(64);index"`
	Scope                  string `json:"scope" gorm:"type:varchar(16);index"`
	ModelName              string `json:"model_name" gorm:"type:text"`
	RequestPath            string `json:"request_path" gorm:"type:varchar(255)"`
	State                  string `json:"state" gorm:"type:varchar(32);index"`
	Reason                 string `json:"reason" gorm:"type:text"`
	OpenedAt               int64  `json:"opened_at" gorm:"bigint"`
	NextProbeAt            int64  `json:"next_probe_at" gorm:"bigint;index"`
	Revision               uint64 `json:"revision" gorm:"bigint"`
	ProbeID                uint64 `json:"probe_id" gorm:"bigint"`
	ProbeType              string `json:"probe_type" gorm:"type:varchar(16)"`
	ProbeLeaseEnd          int64  `json:"probe_lease_end" gorm:"bigint"`
	RecoveryTargetCapacity int    `json:"recovery_target_capacity"`
	RecoveryCapacity       int    `json:"recovery_capacity"`
	RecoverySuccesses      int    `json:"recovery_successes"`
	RecoveryStartedAt      int64  `json:"recovery_started_at" gorm:"bigint"`
	CreatedAt              int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt              int64  `json:"updated_at" gorm:"bigint;index"`
}

func (state *ChannelHealthState) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if state.CreatedAt == 0 {
		state.CreatedAt = now
	}
	state.UpdatedAt = now
	return nil
}

func (state *ChannelHealthState) BeforeUpdate(_ *gorm.DB) error {
	state.UpdatedAt = common.GetTimestamp()
	return nil
}

func SaveChannelHealthState(state *ChannelHealthState) error {
	if state == nil || state.ScopeKey == "" || state.ChannelID <= 0 {
		return errors.New("valid channel health state is required")
	}
	return DB.Save(state).Error
}

func DeleteChannelHealthState(scopeKey string) error {
	if scopeKey == "" {
		return nil
	}
	return DB.Where("scope_key = ?", scopeKey).Delete(&ChannelHealthState{}).Error
}

func DeleteChannelHealthStatesForChannel(channelID int) error {
	if channelID <= 0 {
		return nil
	}
	return DB.Where("channel_id = ?", channelID).Delete(&ChannelHealthState{}).Error
}

func ListChannelHealthStates() ([]ChannelHealthState, error) {
	var states []ChannelHealthState
	err := DB.Order("channel_id asc, scope_key asc").Find(&states).Error
	return states, err
}
