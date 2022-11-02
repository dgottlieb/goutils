// Package oauth contains rpc AuthHandler for oauth/oidc JWT tokens.
package oauth

import (
	"context"
	"crypto/rsa"
	"time"

	"github.com/edaniels/golog"
	"github.com/golang-jwt/jwt/v4"
	"github.com/pkg/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"go.viam.com/utils/jwks"
	"go.viam.com/utils/rpc"
)

const (
	// CredentialsTypeOAuthWeb for jwt access tokens signed by oidc/oauth backend.
	CredentialsTypeOAuthWeb = rpc.CredentialsType("oauth-web-auth")
)

type webOAuthHandler struct {
	WebOAuthOptions
}

var (
	_ rpc.AuthHandler              = &webOAuthHandler{}
	_ rpc.TokenCustomClaimProvider = &webOAuthHandler{}
)

type webOAuthClaims struct {
	rpc.JWTClaims

	// The JWT must contain the aud claim otherwise it must be rejected.
	allowedAudience string
}

func (c *webOAuthClaims) Entity() (string, error) {
	// Claim `sub` will contain the Auth0 userid not the basic email.
	if email, ok := c.AuthMetadata["email"]; ok {
		return email, nil
	}

	return "", errors.New("missing email in rpc_auth_md")
}

func (c *webOAuthClaims) Valid() error {
	if !c.RegisteredClaims.VerifyAudience(c.allowedAudience, true) {
		return errors.New("invalid aud")
	}

	return c.JWTClaims.Valid()
}

var _ rpc.Claims = &webOAuthClaims{}

// WebOAuthOptions options for the WebOauth handler.
type WebOAuthOptions struct {
	// Audience claim that must be within the "aud" JWT claims presented.
	AllowedAudience string

	// Key provider used to provide public keys to validate the jwt basid on its "kid" header.
	KeyProvider jwks.KeyProvider

	// Underlying Entity verifier after
	EntityVerifier func(ctx context.Context, entity string) (interface{}, error)
	Logger         golog.Logger
}

// WithWebOAuthTokenAuthHandler returns a rpc server option configured for the AuthHandler. The WebAuth handler will
// validate jwt access tokens signed by OIDC provider. The jwts are validated for the aud standard claim.
//
// This allows auth handler allows for a dynamic set of signing keys provided through OIDC configuration endpoint.
//
// Entity verification is deleged to the EntityVerifier method in the options.
func WithWebOAuthTokenAuthHandler(opts WebOAuthOptions) rpc.ServerOption {
	authHandler := &webOAuthHandler{
		opts,
	}

	return rpc.WithAuthHandler(CredentialsTypeOAuthWeb, authHandler)
}

// Authenticate always returns an error. Webauth is expecting an access token to be generated by a separate
// system instead of using Authenticate() to create the access token.
func (a *webOAuthHandler) Authenticate(ctx context.Context, entity, payload string) (map[string]string, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented with webauth")
}

// VerifyEntity verifies that this handler is allowed to authenticate the given entity.
// The handler can optionally return opaque info about the entity that will be bound to the
// context accessible via ContextAuthEntity.
func (a *webOAuthHandler) VerifyEntity(ctx context.Context, entity string) (interface{}, error) {
	if a.EntityVerifier == nil {
		return nil, status.Errorf(codes.Internal, "invalid verify entity configuration")
	}
	return a.EntityVerifier(ctx, entity)
}

func (a *webOAuthHandler) CreateClaims() rpc.Claims {
	return &webOAuthClaims{
		// used to valid the aud claim in the jwt.
		allowedAudience: a.AllowedAudience,
	}
}

// TokenVerificationKey returns the rsa public key needed to do JWT verification. Uses
// the jwtkey provided.
func (a *webOAuthHandler) TokenVerificationKey(token *jwt.Token) (ret interface{}, err error) {
	keyID, ok := token.Header["kid"].(string)
	if !ok {
		return nil, errors.New("kid not valid")
	}

	// TODO: We should probably have the context passed from the auth process.
	return a.KeyProvider.LookupKey(context.TODO(), keyID)
}

// SignWebAuthAccessToken returns an access jwt access token typically done by auth0 during access token flow.
func SignWebAuthAccessToken(key *rsa.PrivateKey, entity, aud, iss, keyID string) (string, error) {
	token := &jwt.Token{
		Header: map[string]interface{}{
			"typ": "JWT",
			"alg": jwt.SigningMethodRS256.Alg(),
			"kid": keyID,
		},
		Claims: rpc.JWTClaims{
			RegisteredClaims: jwt.RegisteredClaims{
				Audience: []string{aud},
				Issuer:   iss,
				// in prod this may not be 1:1 to the email. This is usually the user id
				// from auth0
				Subject:  entity,
				IssuedAt: jwt.NewNumericDate(time.Now()),
			},
			CredentialsType: CredentialsTypeOAuthWeb,
			AuthMetadata: map[string]string{
				"email": entity,
			},
		},
		Method: jwt.SigningMethodRS256,
	}

	return token.SignedString(key)
}
