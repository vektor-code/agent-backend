package agent

import (
	"context"

	"github.com/kubetrace/agent-backend/internal/controller"
)

func (a *Agent) RunController(ctx context.Context) {
	controller.Run(ctx, a.centralURL, a.client)
}
