package authutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/oauth2"

	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

const ServiceName = "datum-mcp-auth"
const ActiveUserKey = "active_user"
const KnownUsersKey = "known_users"

var ErrNoActiveUser = errors.New("no active user set. Please login first")

type StoredCredentials struct {
	Hostname         string        `json:"hostname"`
	APIHostname      string        `json:"api_hostname"`
	ClientID         string        `json:"client_id"`
	EndpointAuthURL  string        `json:"endpoint_auth_url"`
	EndpointTokenURL string        `json:"endpoint_token_url"`
	Scopes           []string      `json:"scopes"`
	Token            *oauth2.Token `json:"token"`
	UserName         string        `json:"user_name"`
	UserEmail        string        `json:"user_email"`
	Subject          string        `json:"subject"`
}

func GetActiveCredentials() (*StoredCredentials, string, error) {
	activeUserKey, err := keyring.Get(ServiceName, ActiveUserKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, "", ErrNoActiveUser
		}
		return nil, "", fmt.Errorf("failed to get active user from keyring: %w", err)
	}
	if activeUserKey == "" {
		return nil, "", ErrNoActiveUser
	}
	creds, err := GetStoredCredentials(activeUserKey)
	if err != nil {
		return nil, activeUserKey, err
	}
	return creds, activeUserKey, nil
}

func GetStoredCredentials(userKey string) (*StoredCredentials, error) {
	credsJSON, err := keyring.Get(ServiceName, userKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, fmt.Errorf("credentials for user '%s' not found in keyring", userKey)
		}
		return nil, fmt.Errorf("failed to get credentials for '%s' from keyring: %w", userKey, err)
	}
	if credsJSON == "" {
		return nil, fmt.Errorf("empty credentials found for user '%s' in keyring", userKey)
	}
	var creds StoredCredentials
	if err := json.Unmarshal([]byte(credsJSON), &creds); err != nil {
		return nil, fmt.Errorf("failed to unmarshal credentials for '%s': %w", userKey, err)
	}
	if creds.Token == nil {
		return nil, fmt.Errorf("stored credentials for '%s' are missing token information", userKey)
	}
	return &creds, nil
}

func GetTokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	creds, _, err := GetActiveCredentials()
	if err != nil {
		return nil, err
	}
	conf := &oauth2.Config{
		ClientID: creds.ClientID,
		Scopes:   creds.Scopes,
		Endpoint: oauth2.Endpoint{AuthURL: creds.EndpointAuthURL, TokenURL: creds.EndpointTokenURL},
	}
	return conf.TokenSource(ctx, creds.Token), nil
}

func GetActiveUserKey() (string, error) {
	activeUserKey, err := keyring.Get(ServiceName, ActiveUserKey)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoActiveUser
		}
		return "", fmt.Errorf("failed to get active user from keyring: %w", err)
	}
	if activeUserKey == "" {
		return "", ErrNoActiveUser
	}
	return activeUserKey, nil
}

func GetAPIHostname() (string, error) {
	creds, _, err := GetActiveCredentials()
	if err != nil {
		return "", err
	}
	if creds.APIHostname != "" {
		return creds.APIHostname, nil
	}
	return DeriveAPIHostname(creds.Hostname)
}

func DeriveAPIHostname(authHostname string) (string, error) {
	if authHostname == "" {
		return "", errors.New("cannot derive API hostname from empty auth hostname")
	}
	if strings.HasPrefix(authHostname, "auth.") {
		return "api." + strings.TrimPrefix(authHostname, "auth."), nil
	}
	return "", fmt.Errorf("could not derive API hostname from '%s'", authHostname)
}

// GetSubject returns the current user's subject (user ID) from stored credentials.
func GetSubject() (string, error) {
	creds, _, err := GetActiveCredentials()
	if err != nil {
		return "", err
	}
	if creds.Subject == "" {
		return "", fmt.Errorf("subject (user ID) not found in stored credentials")
	}
	return creds.Subject, nil
}
