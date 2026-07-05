package internal

import (
	"fmt"
	"net/url"
)

func widgetAuthURL(appID string) string {
	return fmt.Sprintf(
		"https://discord.com/oauth2/authorize?client_id=%s&redirect_uri=%s&response_type=token&scope=openid+sdk.social_layer",
		appID,
		url.QueryEscape("https://discord.com"),
	)
}
