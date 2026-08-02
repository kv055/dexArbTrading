package mmcapabilities

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func evaluate(provider Provider, account accountResult, policy Policy, now time.Time, maxAge time.Duration) ProviderState {
	state := ProviderState{
		AccountID:          account.id,
		Provider:           provider.Name,
		Exchange:           provider.ID,
		CostSupported:      provider.CostSupported,
		ExecutionSupported: provider.ExecutionSupported,
		ActiveKeyCount:     len(account.keys),
		Policy:             policy,
	}
	for _, key := range account.keys {
		if key.checkedAt != nil && (state.PermissionsCheckedAt == nil || key.checkedAt.After(*state.PermissionsCheckedAt)) {
			checked := key.checkedAt.UTC()
			state.PermissionsCheckedAt = &checked
		}
	}
	state.Spot = evaluateLeg(provider.ExecutionSupported && provider.SpotSupported, policy.SpotEnabled, account, spotPermissions, now, maxAge)
	state.Hedge = evaluateLeg(provider.ExecutionSupported && provider.HedgeSupported, policy.HedgeEnabled, account, hedgePermissions, now, maxAge)
	return state
}

func evaluateLeg(supported, policyEnabled bool, account accountResult, required []string, now time.Time, maxAge time.Duration) LegState {
	leg := LegState{Supported: supported, PolicyEnabled: policyEnabled, BlockingPermissions: []string{}, Blockers: []Blocker{}}
	if !supported {
		leg.Blockers = append(leg.Blockers, Blocker{Code: BlockUnsupported, Message: "MM execution does not support this provider capability."})
	}
	if !policyEnabled {
		leg.Blockers = append(leg.Blockers, Blocker{Code: BlockPolicyDisabled, Message: "Disabled by the operator policy."})
	}
	if account.id == 0 {
		leg.Blockers = append(leg.Blockers, Blocker{Code: BlockNoAccount, Message: "No trading account is configured."})
	} else if len(account.keys) == 0 {
		leg.Blockers = append(leg.Blockers, Blocker{Code: BlockNoActiveKeys, Message: "No active API keys are configured."})
	}

	missing := make([]string, 0, len(required))
	for _, permission := range required {
		if !permissionGranted(permission, account.keys, now, maxAge) {
			missing = append(missing, permission)
		}
	}
	leg.BlockingPermissions = missing
	if len(missing) > 0 && account.id != 0 && len(account.keys) > 0 {
		code, message := permissionBlocker(account.keys, now, maxAge)
		leg.Blockers = append(leg.Blockers, Blocker{Code: code, Permissions: missing, Message: message})
	}
	leg.Available = supported && account.id != 0 && len(account.keys) > 0 && len(missing) == 0
	leg.EffectiveEnabled = leg.Available && policyEnabled
	return leg
}

func permissionGranted(permission string, keys []keyResult, now time.Time, maxAge time.Duration) bool {
	for _, key := range keys {
		if strings.TrimSpace(key.checkError) != "" || key.checkedAt == nil || now.Sub(key.checkedAt.UTC()) > maxAge {
			continue
		}
		if key.permissions[permission] == StatusGranted {
			return true
		}
	}
	return false
}

func permissionBlocker(keys []keyResult, now time.Time, maxAge time.Duration) (string, string) {
	for _, key := range keys {
		if strings.TrimSpace(key.checkError) != "" {
			return BlockCheckFailed, "The latest API-key permission check failed."
		}
	}
	for _, key := range keys {
		if key.checkedAt == nil || now.Sub(key.checkedAt.UTC()) > maxAge {
			return BlockStale, "API-key permissions have not been verified recently."
		}
	}
	return BlockMissingPermission, "Required API-key permissions were not granted."
}

func decodePermissions(raw []byte) map[string]string {
	if len(raw) == 0 {
		return map[string]string{}
	}
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return map[string]string{}
	}
	result := make(map[string]string, len(values))
	for name, value := range values {
		switch typed := value.(type) {
		case string:
			result[name] = strings.ToLower(strings.TrimSpace(typed))
		case bool:
			if typed {
				result[name] = StatusGranted
			} else {
				result[name] = StatusDenied
			}
		default:
			result[name] = StatusUnknown
		}
	}
	return result
}

func invalidPermissionsError(raw []byte) string {
	if len(raw) == 0 {
		return "permission data is missing"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Sprintf("invalid permission data: %v", err)
	}
	return ""
}
