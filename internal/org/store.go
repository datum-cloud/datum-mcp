package org

import (
	"errors"
	"fmt"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

const activeOrgKey = "active_org"

func GetActive() (string, error) {
	o, err := keyring.Get(authutil.ServiceName, activeOrgKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", nil
		}
		return "", fmt.Errorf("failed to read active organization: %w", err)
	}
	return o, nil
}

func SetActive(name string) error {
	if name == "" {
		return fmt.Errorf("organization cannot be empty")
	}
	return keyring.Set(authutil.ServiceName, activeOrgKey, name)
}
