package model

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

var ErrCanvasOAuthCodeInvalid = errors.New("canvas oauth code invalid or expired")

type CanvasOAuthCode struct {
	Id            int    `json:"id"`
	CodeHash      string `json:"-" gorm:"type:char(64);uniqueIndex"`
	UserId        int    `json:"user_id" gorm:"index"`
	ClientId      string `json:"client_id" gorm:"type:varchar(128);index"`
	RedirectUri   string `json:"redirect_uri" gorm:"type:text"`
	CodeChallenge string `json:"code_challenge" gorm:"type:varchar(128)"`
	CreatedAt     int64  `json:"created_at" gorm:"bigint"`
	ExpiresAt     int64  `json:"expires_at" gorm:"bigint;index"`
	UsedAt        int64  `json:"used_at" gorm:"bigint;default:0;index"`
}

func CreateCanvasOAuthCode(code *CanvasOAuthCode) error {
	if code == nil {
		return errors.New("canvas oauth code is nil")
	}
	now := time.Now().Unix()
	_ = DB.Where("expires_at < ? OR used_at > 0", now-300).Delete(&CanvasOAuthCode{}).Error
	return DB.Create(code).Error
}

func ConsumeCanvasOAuthCode(codeHash, clientId, redirectUri, codeChallenge string, now int64) (*CanvasOAuthCode, error) {
	var code CanvasOAuthCode
	err := DB.Where("code_hash = ? AND client_id = ? AND redirect_uri = ? AND code_challenge = ?", codeHash, clientId, redirectUri, codeChallenge).First(&code).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrCanvasOAuthCodeInvalid
		}
		return nil, err
	}
	if code.UsedAt != 0 || code.ExpiresAt < now {
		return nil, ErrCanvasOAuthCodeInvalid
	}
	result := DB.Model(&CanvasOAuthCode{}).
		Where("id = ? AND used_at = 0 AND expires_at >= ?", code.Id, now).
		Update("used_at", now)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, ErrCanvasOAuthCodeInvalid
	}
	code.UsedAt = now
	return &code, nil
}
