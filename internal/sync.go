package internal

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

type dynamicField struct {
	Type  int    `json:"type"`
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type widgetPayload struct {
	Username string `json:"username"`
	Data     struct {
		Dynamic []dynamicField `json:"dynamic"`
	} `json:"data"`
}

const autoSyncInterval = 30 * time.Minute

func (b *Bot) startAutoSync() {
	go func() {
		t := time.NewTicker(autoSyncInterval)
		defer t.Stop()
		b.syncAll()
		for range t.C {
			b.syncAll()
		}
	}()
}

func (b *Bot) syncAll() {
	links := b.store.All()
	if len(links) == 0 {
		return
	}
	wikis := make(map[string]WikiInfo)
	for _, link := range links {
		wiki, ok := wikis[link.Wiki]
		if !ok {
			var err error
			wiki, err = b.fandom.GetWikiInfo(link.Wiki)
			if err != nil {
				log.Printf("auto sync wiki %s: %v", link.Wiki, err)
				continue
			}
			wikis[link.Wiki] = wiki
		}
		if err := b.syncLink(link, wiki); err != nil {
			log.Printf("auto sync %s: %v", link.DiscordID, err)
		}
	}
}

func (b *Bot) syncLink(link UserLink, wiki WikiInfo) error {
	profile, err := b.fandom.GetProfile(link.Wiki, link.UserID)
	if err != nil {
		return err
	}
	return syncWidget(b.appID, b.token, link.DiscordID, wiki, profile)
}

func syncWidget(appID, token, discordID string, wiki WikiInfo, p UserProfile) error {
	display := p.DisplayName
	if display == "" {
		display = p.Username
	}
	tags := strings.Join(p.Tags, ", ")
	if tags == "" {
		tags = "Editor"
	}

	fields := []dynamicField{
		{Type: 1, Name: "display_name", Value: display},
		{Type: 1, Name: "wiki_username", Value: p.Username},
		{Type: 1, Name: "wiki", Value: "@" + wiki.Subdomain},
		{Type: 1, Name: "wiki_name", Value: wiki.Name},
		{Type: 1, Name: "edits", Value: p.Edits},
		{Type: 2, Name: "edit_count", Value: p.LocalEdits},
		{Type: 2, Name: "edit_goal", Value: editGoal(p.LocalEdits)},
		{Type: 2, Name: "posts", Value: p.Posts},
		{Type: 1, Name: "registered", Value: p.Registration},
		{Type: 1, Name: "tags", Value: tags},
	}
	if p.Bio != "" {
		fields = append(fields, dynamicField{Type: 1, Name: "bio", Value: truncate(p.Bio, 120)})
	}
	if p.Avatar != "" {
		fields = append(fields, dynamicField{
			Type:  3,
			Name:  "avatar",
			Value: map[string]string{"url": webpURL(p.Avatar)},
		})
	}
	if wiki.Logo != "" {
		fields = append(fields, dynamicField{
			Type:  3,
			Name:  "wiki_logo",
			Value: map[string]string{"url": webpURL(wiki.Logo)},
		})
	}
	if wiki.Favicon != "" {
		fields = append(fields, dynamicField{
			Type:  3,
			Name:  "wiki_favicon",
			Value: map[string]string{"url": webpURL(wiki.Favicon)},
		})
	}

	var payload widgetPayload
	payload.Username = p.Username
	payload.Data.Dynamic = fields

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("https://discord.com/api/v9/applications/%s/users/%s/identities/0/profile", appID, discordID)
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bot "+token)
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/discord/discord-api-docs, 1.0.0)")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		var buf bytes.Buffer
		buf.ReadFrom(res.Body)
		return fmt.Errorf("discord sync %s: %w", res.Status, parseDiscordAPIError(buf.Bytes()))
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func webpURL(u string) string {
	if u == "" {
		return ""
	}
	if strings.Contains(u, "?") {
		return u + "&format=webp"
	}
	return u + "?format=webp"
}

func editGoal(edits int) int {
	for _, g := range []int{50, 100, 250, 500, 1000, 2500, 5000} {
		if edits < g {
			return g
		}
	}
	return (edits/5000 + 1) * 5000
}

type discordAPIError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

func parseDiscordAPIError(body []byte) error {
	var api discordAPIError
	if json.Unmarshal(body, &api) != nil || api.Message == "" {
		return fmt.Errorf("%s", body)
	}
	switch api.Code {
	case 20012:
		return fmt.Errorf("%s (code %d): bot token does not belong to this application", api.Message, api.Code)
	case 50001, 50026:
		return fmt.Errorf("%s (code %d): open widget auth URL printed at bot startup", api.Message, api.Code)
	default:
		return fmt.Errorf("%s (code %d)", api.Message, api.Code)
	}
}
