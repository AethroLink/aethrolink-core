package api

import (
	"errors"

	"github.com/aethrolink/aethrolink-core/internal/agents"
	"github.com/aethrolink/aethrolink-core/internal/core"
)

func errorStatus(err error) int {
	switch {
	case errors.Is(err, agents.ErrAgentNotFound):
		return 404
	default:
		return core.ErrorStatus(err)
	}
}
