package developer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"vocat/internal/httpsmode"
	"vocat/internal/store"
)

func Enabled(ctx context.Context, database *store.Store) bool {
	setting, err := database.AppSetting(ctx, EnabledSettingKey)
	if err != nil {
		return false
	}
	var document struct {
		Enabled bool `json:"enabled"`
	}
	return json.Unmarshal(setting.Value, &document) == nil && document.Enabled
}

const (
	EnabledSettingKey     = "developer.enabled"
	DeviceLimitSettingKey = "developer.device_limit"
	SMSHourlyLimitKey     = "developer.sms_hourly_limit"
	DefaultDeviceLimit    = 5
	MaxDeviceLimit        = 10
	DefaultSMSHourlyLimit = 10
	MaxSMSHourlyLimit     = 20
)

func DeviceLimit(ctx context.Context, database *store.Store, enabled bool) int {
	if !enabled {
		return DefaultDeviceLimit
	}
	setting, err := database.AppSetting(ctx, DeviceLimitSettingKey)
	if err != nil {
		return DefaultDeviceLimit
	}
	var document struct {
		Limit int `json:"limit"`
	}
	if json.Unmarshal(setting.Value, &document) != nil || document.Limit < 1 {
		return DefaultDeviceLimit
	}
	if document.Limit > MaxDeviceLimit {
		return MaxDeviceLimit
	}
	return document.Limit
}

func SetDeviceLimit(ctx context.Context, database *store.Store, limit int) error {
	if limit < 1 || limit > MaxDeviceLimit {
		return fmt.Errorf("device limit must be between 1 and %d", MaxDeviceLimit)
	}
	value, err := json.Marshal(map[string]int{"limit": limit})
	if err != nil {
		return err
	}
	return database.UpsertAppSetting(ctx, store.AppSetting{Key: DeviceLimitSettingKey, Value: value})
}

// SMSHourlyLimit is enforced regardless of developer mode. Developer mode
// only controls whether administrators can see and modify this value.
func SMSHourlyLimit(ctx context.Context, database *store.Store) int {
	setting, err := database.AppSetting(ctx, SMSHourlyLimitKey)
	if err != nil {
		return DefaultSMSHourlyLimit
	}
	var document struct {
		Limit int `json:"limit"`
	}
	if json.Unmarshal(setting.Value, &document) != nil || document.Limit < 1 {
		return DefaultSMSHourlyLimit
	}
	if document.Limit > MaxSMSHourlyLimit {
		return MaxSMSHourlyLimit
	}
	return document.Limit
}

func SetSMSHourlyLimit(ctx context.Context, database *store.Store, limit int) error {
	if limit < 1 || limit > MaxSMSHourlyLimit {
		return fmt.Errorf("SMS hourly limit must be between 1 and %d", MaxSMSHourlyLimit)
	}
	value, err := json.Marshal(map[string]int{"limit": limit})
	if err != nil {
		return err
	}
	return database.UpsertAppSetting(ctx, store.AppSetting{Key: SMSHourlyLimitKey, Value: value})
}

// ResetExperimental restores developer-only experimental settings. It is
// called both by `vocat develop off` and at startup whenever developer mode is
// disabled. Roaming data and export-proxy configurations are first-class
// product features and are left untouched.
func ResetExperimental(ctx context.Context, database *store.Store) error {
	httpsValue, err := json.Marshal(map[string]bool{"enabled": false})
	if err != nil {
		return err
	}
	var resetErrors []error
	if err := database.UpsertAppSetting(ctx, store.AppSetting{Key: httpsmode.SettingKey, Value: httpsValue}); err != nil {
		resetErrors = append(resetErrors, fmt.Errorf("reset self-signed HTTPS: %w", err))
	}
	if err := SetDeviceLimit(ctx, database, DefaultDeviceLimit); err != nil {
		resetErrors = append(resetErrors, fmt.Errorf("reset device limit: %w", err))
	}
	if err := SetSMSHourlyLimit(ctx, database, DefaultSMSHourlyLimit); err != nil {
		resetErrors = append(resetErrors, fmt.Errorf("reset SMS hourly limit: %w", err))
	}
	return errors.Join(resetErrors...)
}
