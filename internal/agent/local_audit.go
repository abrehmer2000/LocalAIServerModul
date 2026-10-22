package agent

import (
	"context"
	"errors"
)

// AuditLocal records a local operator's management intent under this device identity.
func (r *Runner) AuditLocal(ctx context.Context, action string) error {
	c, base, err := r.gatewayClient()
	if err != nil {
		return err
	}
	defer c.CloseIdleConnections()
	var result map[string]bool
	if err = post(ctx, c, base+"/internal/agent-events", map[string]string{"action": action}, &result); err != nil {
		return errors.New("management_audit_unavailable")
	}
	return nil
}
