package keycloak

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Config struct {
	IssuerURL    string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	StepUpACR    string
}

type Client struct {
	oauth     oauth2.Config
	verifier  *oidc.IDTokenVerifier
	stepUpACR string
}

// LoginFlow contains PKCE verifier material and must be retained server-side between redirects.
// Only AuthorizationURL and State are sent through the browser; never log or expose CodeVerifier.
type LoginFlow struct {
	State              string
	Nonce              string
	CodeVerifier       string
	RequestedStepUpACR string
	AuthorizationURL   string
}

type TokenSet struct {
	AccessToken string
	IDToken     string
	TokenType   string
	ExpiresAt   time.Time
	Subject     string
	TenantID    string
	TenantIDs   []string
	SID         string
	ACR         string
	AuthTime    time.Time
}

func NewClient(ctx context.Context, config Config) (*Client, error) {
	issuer, err := url.Parse(config.IssuerURL)
	if err != nil || issuer.Host == "" || (issuer.Scheme != "https" && !(issuer.Scheme == "http" && loopback(issuer.Hostname()))) {
		return nil, errors.New("Keycloak issuer must use HTTPS except for a loopback test issuer")
	}
	if config.ClientID == "" || config.RedirectURL == "" || config.StepUpACR == "" {
		return nil, errors.New("Keycloak client id, redirect URL, and step-up ACR are required")
	}
	redirect, err := url.Parse(config.RedirectURL)
	if err != nil || redirect.Scheme != "https" && !(redirect.Scheme == "http" && loopback(redirect.Hostname())) {
		return nil, errors.New("OIDC redirect URL must use HTTPS except for a loopback test redirect")
	}
	provider, err := oidc.NewProvider(ctx, config.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover Keycloak issuer: %w", err)
	}
	return &Client{
		oauth: oauth2.Config{
			ClientID: config.ClientID, ClientSecret: config.ClientSecret, RedirectURL: config.RedirectURL,
			Endpoint: provider.Endpoint(), Scopes: []string{oidc.ScopeOpenID, oidc.ScopeProfile},
		},
		verifier:  provider.VerifierContext(ctx, &oidc.Config{ClientID: config.ClientID, SupportedSigningAlgs: []string{"RS256"}}),
		stepUpACR: config.StepUpACR,
	}, nil
}

func (c *Client) Begin(stepUp bool) (LoginFlow, error) {
	if c == nil || c.verifier == nil || c.stepUpACR == "" {
		return LoginFlow{}, errors.New("Keycloak client is not configured")
	}
	state, err := randomURLSafe(32)
	if err != nil {
		return LoginFlow{}, err
	}
	nonce, err := randomURLSafe(32)
	if err != nil {
		return LoginFlow{}, err
	}
	verifier, err := randomURLSafe(32)
	if err != nil {
		return LoginFlow{}, err
	}
	flow := LoginFlow{State: state, Nonce: nonce, CodeVerifier: verifier}
	options := []oauth2.AuthCodeOption{
		oauth2.S256ChallengeOption(verifier),
		oauth2.SetAuthURLParam("nonce", nonce),
	}
	if stepUp {
		flow.RequestedStepUpACR = c.stepUpACR
		options = append(options,
			oauth2.SetAuthURLParam("acr_values", c.stepUpACR),
			oauth2.SetAuthURLParam("max_age", "0"),
			oauth2.SetAuthURLParam("prompt", "login"),
		)
	}
	flow.AuthorizationURL = c.oauth.AuthCodeURL(state, options...)
	return flow, nil
}

func (c *Client) Exchange(ctx context.Context, code, returnedState string, flow LoginFlow) (TokenSet, error) {
	if c == nil || c.verifier == nil || code == "" || flow.State == "" || flow.Nonce == "" || flow.CodeVerifier == "" ||
		len(returnedState) != len(flow.State) || subtle.ConstantTimeCompare([]byte(returnedState), []byte(flow.State)) != 1 {
		return TokenSet{}, errors.New("OIDC callback state or PKCE flow is invalid")
	}
	token, err := c.oauth.Exchange(ctx, code, oauth2.VerifierOption(flow.CodeVerifier))
	if err != nil {
		return TokenSet{}, fmt.Errorf("exchange OIDC authorization code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return TokenSet{}, errors.New("Keycloak token response is missing an ID token")
	}
	idToken, err := c.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		return TokenSet{}, fmt.Errorf("verify Keycloak ID token: %w", err)
	}
	if len(idToken.Nonce) != len(flow.Nonce) || subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(flow.Nonce)) != 1 {
		return TokenSet{}, errors.New("Keycloak ID token nonce does not match the login flow")
	}
	claims := struct {
		TenantID  string   `json:"tenant_id"`
		TenantIDs []string `json:"tenant_ids"`
		SID       string   `json:"sid"`
		ACR       string   `json:"acr"`
		AuthTime  int64    `json:"auth_time"`
	}{}
	if err := idToken.Claims(&claims); err != nil {
		return TokenSet{}, fmt.Errorf("decode Keycloak identity claims: %w", err)
	}
	if len(claims.TenantIDs) == 0 && claims.TenantID != "" {
		claims.TenantIDs = []string{claims.TenantID}
	}
	if idToken.Subject == "" || len(claims.TenantIDs) == 0 || claims.SID == "" || claims.ACR == "" {
		return TokenSet{}, fmt.Errorf("Keycloak ID token is missing required identity claims (subject=%t tenant_membership=%t sid=%t acr=%t)",
			idToken.Subject != "", len(claims.TenantIDs) > 0, claims.SID != "", claims.ACR != "")
	}
	if claims.TenantID == "" {
		claims.TenantID = claims.TenantIDs[0]
	}
	if !contains(claims.TenantIDs, claims.TenantID) {
		return TokenSet{}, errors.New("Keycloak primary tenant is not in the tenant membership claim")
	}
	if flow.RequestedStepUpACR != "" && claims.ACR != flow.RequestedStepUpACR {
		return TokenSet{}, errors.New("Keycloak did not satisfy the requested step-up ACR")
	}
	if flow.RequestedStepUpACR != "" && claims.AuthTime == 0 {
		return TokenSet{}, errors.New("Keycloak step-up response is missing auth_time")
	}
	var authTime time.Time
	if claims.AuthTime != 0 {
		authTime = time.Unix(claims.AuthTime, 0).UTC()
	}
	return TokenSet{
		AccessToken: token.AccessToken, IDToken: rawIDToken, TokenType: token.TokenType,
		ExpiresAt: token.Expiry, Subject: idToken.Subject, TenantID: claims.TenantID, TenantIDs: append([]string(nil), claims.TenantIDs...), SID: claims.SID,
		ACR: claims.ACR, AuthTime: authTime,
	}, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func randomURLSafe(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create OIDC PKCE material: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func loopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

// ProofKeyChallenge exposes only the non-secret challenge for diagnostics and tests.
func ProofKeyChallenge(verifier string) string {
	digest := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}
