package project

import (
	"errors"
	"fmt"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

const activeProjectKey = "active_project"

func GetActive() (string, error) {
	p, err := keyring.Get(authutil.ServiceName, activeProjectKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read active project: %w", err)
	}
	return p, nil
}

func SetActive(name string) error {
	if name == "" {
		return fmt.Errorf("project name cannot be empty")
	}
	return keyring.Set(authutil.ServiceName, activeProjectKey, name)
}
