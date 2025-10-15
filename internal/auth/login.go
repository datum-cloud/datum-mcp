package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/pkg/browser"
	"golang.org/x/oauth2"

	"github.com/datum-cloud/datum-mcp/internal/authutil"
	"github.com/datum-cloud/datum-mcp/internal/keyring"
)

const redirectPath = "/datumctl/auth/callback"
const listenAddr = "127.0.0.1:0"

const stagingClientID = "325848904128073754"
const prodClientID = "328728232771788043"

func generateCodeVerifier() (string, error) {
	const length = 64
	randomBytes := make([]byte, length)
	_, err := rand.Read(randomBytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(randomBytes), nil
}

func generateCodeChallenge(verifier string) string {
	hash := sha256.Sum256([]byte(verifier))
	return base64.URLEncoding.WithPadding(base64.NoPadding).EncodeToString(hash[:])
}

func generateRandomState(length int) (string, error) {
	b := make([]byte, length)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func defaultHostnames() (authHost string, apiHost string) {
	authHost = getenvDefault("DATUM_AUTH_HOSTNAME", "auth.datum.net")
	apiHost = os.Getenv("DATUM_API_HOSTNAME")
	return
}

func resolveClientID(hostname string) (string, error) {
	if v := os.Getenv("DATUM_CLIENT_ID"); v != "" {
		return v, nil
	}
	if strings.HasSuffix(hostname, ".staging.env.datum.net") {
		return stagingClientID, nil
	}
	if strings.HasSuffix(hostname, ".datum.net") {
		return prodClientID, nil
	}
	return "", fmt.Errorf("client ID not configured for hostname '%s'. Set DATUM_CLIENT_ID", hostname)
}

func getenvDefault(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// RunLoginFlow performs the PKCE OAuth2 login and stores credentials in keyring.
func RunLoginFlow(ctx context.Context, verbose bool) error {
	authHostname, apiHostname := defaultHostnames()
	clientID, err := resolveClientID(authHostname)
	if err != nil {
		return err
	}

	log.Printf("Starting login for %s...", authHostname)

	var finalAPIHostname string
	if apiHostname != "" {
		finalAPIHostname = apiHostname
		log.Printf("Using API hostname: %s", finalAPIHostname)
	} else {
		derivedAPI, err := authutil.DeriveAPIHostname(authHostname)
		if err != nil {
			return fmt.Errorf("failed to derive API hostname: %w", err)
		}
		finalAPIHostname = derivedAPI
		log.Printf("Derived API hostname: %s", finalAPIHostname)
	}

	providerURL := fmt.Sprintf("https://%s", authHostname)
	provider, err := oidc.NewProvider(ctx, providerURL)
	if err != nil {
		return fmt.Errorf("failed to discover OIDC provider at %s: %w", providerURL, err)
	}

	scopes := []string{oidc.ScopeOpenID, "profile", "email", oidc.ScopeOfflineAccess}

	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", listenAddr, err)
	}
	defer listener.Close()

	actualListenAddr := listener.Addr().String()

	conf := &oauth2.Config{
		ClientID:    clientID,
		Scopes:      scopes,
		Endpoint:    provider.Endpoint(),
		RedirectURL: fmt.Sprintf("http://%s%s", actualListenAddr, redirectPath),
	}

	codeVerifier, err := generateCodeVerifier()
	if err != nil {
		return fmt.Errorf("failed to generate code verifier: %w", err)
	}
	codeChallenge := generateCodeChallenge(codeVerifier)

	state, err := generateRandomState(32)
	if err != nil {
		return fmt.Errorf("failed to generate state: %w", err)
	}

	authURL := conf.AuthCodeURL(state,
		oauth2.SetAuthURLParam("code_challenge", codeChallenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)

	codeChan := make(chan string)
	errChan := make(chan error)
	serverClosed := make(chan struct{})

	server := &http.Server{}
	mux := http.NewServeMux()
	mux.HandleFunc(redirectPath, func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		receivedState := r.URL.Query().Get("state")
		if receivedState != state {
			http.Error(w, "Invalid state parameter", http.StatusBadRequest)
			errChan <- fmt.Errorf("invalid state parameter")
			return
		}
		if code == "" {
			errMsg := r.URL.Query().Get("error_description")
			if errMsg == "" {
				errMsg = "Authorization code not found"
			}
			http.Error(w, errMsg, http.StatusBadRequest)
			errChan <- errors.New(errMsg)
			return
		}
		http.Redirect(w, r, "https://docs.datum.net", http.StatusFound)
		codeChan <- code
	})
	server.Handler = mux

	go func() {
		if err := server.Serve(listener); err != http.ErrServerClosed {
			select {
			case <-ctx.Done():
			default:
				errChan <- fmt.Errorf("failed to start callback server: %w", err)
			}
		}
	}()

	log.Println("Opening browser for authentication...")
	log.Printf("Open this URL if the browser doesn't open: %s", authURL)
	_ = browser.OpenURL(authURL)

	var authCode string
	select {
	case code := <-codeChan:
		authCode = code
		go func() { _ = server.Shutdown(context.Background()); close(serverClosed) }()
	case err := <-errChan:
		return fmt.Errorf("authentication failed: %w", err)
	case <-ctx.Done():
		go server.Shutdown(context.Background())
		return ctx.Err()
	}

	token, err := conf.Exchange(ctx, authCode, oauth2.SetAuthURLParam("code_verifier", codeVerifier))
	if err != nil {
		<-serverClosed
		return fmt.Errorf("failed to exchange code for token: %w", err)
	}
	<-serverClosed

	idTokenString, ok := token.Extra("id_token").(string)
	if !ok {
		return fmt.Errorf("id_token not found in token response")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: clientID}).Verify(ctx, idTokenString)
	if err != nil {
		return fmt.Errorf("failed to verify ID token: %w", err)
	}
	var claims struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return fmt.Errorf("failed to extract claims: %w", err)
	}
	if claims.Subject == "" || claims.Email == "" {
		return fmt.Errorf("missing required claims in ID token")
	}

	log.Printf("Authenticated as: %s (%s)", claims.Name, claims.Email)

	userKey := claims.Email
	creds := authutil.StoredCredentials{
		Hostname:         authHostname,
		APIHostname:      finalAPIHostname,
		ClientID:         clientID,
		EndpointAuthURL:  provider.Endpoint().AuthURL,
		EndpointTokenURL: provider.Endpoint().TokenURL,
		Scopes:           scopes,
		Token:            token,
		UserName:         claims.Name,
		UserEmail:        claims.Email,
		Subject:          claims.Subject,
	}
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to serialize credentials: %w", err)
	}
	if err := keyring.Set(authutil.ServiceName, userKey, string(credsJSON)); err != nil {
		return fmt.Errorf("failed to store credentials: %w", err)
	}
	if err := keyring.Set(authutil.ServiceName, authutil.ActiveUserKey, userKey); err != nil {
		log.Printf("Warning: failed to set active user: %v", err)
	}
	if err := addKnownUser(userKey); err != nil {
		log.Printf("Warning: failed to update known users: %v", err)
	}

	if verbose {
		var raw map[string]any
		if err := idToken.Claims(&raw); err == nil {
			b, _ := json.MarshalIndent(raw, "", "  ")
			log.Println(string(b))
		}
	}
	return nil
}

func addKnownUser(newUserKey string) error {
	knownUsers := []string{}
	knownUsersJSON, err := keyring.Get(authutil.ServiceName, authutil.KnownUsersKey)
	if err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("failed to get known users list: %w", err)
	}
	if err == nil && knownUsersJSON != "" {
		if err := json.Unmarshal([]byte(knownUsersJSON), &knownUsers); err != nil {
			return fmt.Errorf("failed to unmarshal known users list: %w", err)
		}
	}
	found := false
	for _, k := range knownUsers {
		if k == newUserKey {
			found = true
			break
		}
	}
	if !found {
		knownUsers = append(knownUsers, newUserKey)
		updatedJSON, err := json.Marshal(knownUsers)
		if err != nil {
			return fmt.Errorf("failed to marshal known users: %w", err)
		}
		if err := keyring.Set(authutil.ServiceName, authutil.KnownUsersKey, string(updatedJSON)); err != nil {
			return fmt.Errorf("failed to store known users: %w", err)
		}
	}
	return nil
}
