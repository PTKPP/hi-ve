package identity

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

var ErrInvalidProof = errors.New("invalid Google proof")

const googleIssuer = "https://accounts.google.com"

type GoogleIdentity struct {
	issuer, subject string
}

func (p GoogleIdentity) valid() bool { return p.issuer == googleIssuer && p.subject != "" }

type Google struct {
	config   oauth2.Config
	verifier *oidc.IDTokenVerifier
}

func NewGoogle(ctx context.Context, id, secret, redirect string) (*Google, error) {
	provider, err := oidc.NewProvider(ctx, googleIssuer)
	if err != nil {
		return nil, err
	}
	return &Google{
		config:   oauth2.Config{ClientID: id, ClientSecret: secret, RedirectURL: redirect, Endpoint: provider.Endpoint(), Scopes: []string{oidc.ScopeOpenID}},
		verifier: provider.Verifier(&oidc.Config{ClientID: id, SupportedSigningAlgs: []string{"RS256"}}),
	}, nil
}

func (g *Google) AuthorizationURL(state, nonce, verifier string) string {
	return g.config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
}

func (g *Google) Exchange(ctx context.Context, code, nonce, verifier string) (GoogleIdentity, error) {
	tokens, err := g.config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return GoogleIdentity{}, ErrInvalidProof
	}
	raw, ok := tokens.Extra("id_token").(string)
	if !ok {
		return GoogleIdentity{}, ErrInvalidProof
	}
	return g.verify(ctx, raw, nonce)
}

func (g *Google) verify(ctx context.Context, raw, nonce string) (GoogleIdentity, error) {
	token, err := g.verifier.Verify(ctx, raw)
	if err != nil || nonce == "" || token.Nonce != nonce || token.Subject == "" ||
		token.IssuedAt.IsZero() || token.IssuedAt.After(time.Now().Add(time.Minute)) ||
		len(token.Subject) > 255 || strings.IndexFunc(token.Subject, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return GoogleIdentity{}, ErrInvalidProof
	}
	var claims struct {
		AuthorizedParty string `json:"azp"`
	}
	if err := token.Claims(&claims); err != nil ||
		(claims.AuthorizedParty != "" && claims.AuthorizedParty != g.config.ClientID) ||
		(len(token.Audience) > 1 && claims.AuthorizedParty != g.config.ClientID) {
		return GoogleIdentity{}, ErrInvalidProof
	}
	return GoogleIdentity{issuer: googleIssuer, subject: token.Subject}, nil
}
