package artifactory

import (
	"context"

	jwt "github.com/golang-jwt/jwt/v4"
	"github.com/hashicorp/vault/sdk/framework"
	"github.com/hashicorp/vault/sdk/logical"
)

func (b *backend) pathConfigRotate() *framework.Path {
	return &framework.Path{
		Pattern: "config/rotate",
		Operations: map[logical.Operation]framework.OperationHandler{
			logical.UpdateOperation: &framework.PathOperation{
				Callback: b.pathConfigRotateWrite,
				Summary:  "Rotate the Artifactory Admin Token.",
			},
		},
		HelpSynopsis: `Rotate the Artifactory Admin Token.`,
		HelpDescription: `
This will rotate the "access_token" used to access artifactory from this plugin, and remove the old token.
`,
	}
}

func (b *backend) pathConfigRotateWrite(ctx context.Context, req *logical.Request, data *framework.FieldData) (*logical.Response, error) {
	b.configMutex.Lock()
	defer b.configMutex.Unlock()

	config, err := b.fetchAdminConfiguration(ctx, req.Storage)
	if err != nil {
		return nil, err
	}

	if config == nil {
		return logical.ErrorResponse("backend not configured"), nil
	}

	oldAccessToken := config.AccessToken

	// Parse Current Token (to get tokenID/scope)
	// -- NOTE THIS IGNORES THE SIGNATURE, which is probably bad,
	//    but it is artifactory's job to validate the token, right?
	p := jwt.Parser{}
	token, _, err := p.ParseUnverified(oldAccessToken, jwt.MapClaims{})
	if err != nil {
		return logical.ErrorResponse("error parsing existing AccessToken: ", err.Error()), nil
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return logical.ErrorResponse("error parsing claims in existing AccessToken"), nil
	}
	// SKIP: We are not validating the token, so it won't be valid :)
	// if !token.Valid {
	// 	b.Logger().Warn("While rotating, existing token seems to be invalid")
	//  return logical.ErrorResponse("error parsing claims in existing AccessToken"), nil
	// }
	oldTokenID := claims["jti"].(string) // jti -> JFrog Token ID
	scope := claims["scp"].(string)      // scp -> scope
	b.Logger().Debug("oldTokenID: " + oldTokenID)

	// Create admin role for the new token
	role := &artifactoryRole{
		Username: "admin",
		Scope:    scope,
	}

	// Create a new token
	resp, err := b.createToken(*config, *role)
	if err != nil {
		return logical.ErrorResponse("error parsing claims in existing AccessToken"), err
	}
	b.Logger().Debug("newTokenID: " + resp.TokenId)

	// Set new token
	config.AccessToken = resp.AccessToken

	// Save new config
	entry, err := logical.StorageEntryJSON("config/admin", config)
	if err != nil {
		return nil, err
	}

	err = req.Storage.Put(ctx, entry)
	if err != nil {
		return nil, err
	}

	// Invalidate Old Token (TODO)
	oldSecret := logical.Secret{
		InternalData: map[string]interface{}{
			"access_token": oldAccessToken,
			"token_id":     oldTokenID,
		},
	}
	err = b.revokeToken(*config, oldSecret)
	if err != nil {
		return logical.ErrorResponse("error revoking existing AccessToken"), err
	}

	return nil, nil
}
