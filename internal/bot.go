package internal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
)

type Bot struct {
	appID  string
	token  string
	store  *Store
	fandom *FandomClient
}

var widgetCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "widget",
		Description: "Fandom wiki profile widget",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "setup",
				Description: "Link your Fandom wiki profile",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "wiki",
						Description: "Wiki subdomain or URL (e.g. tds or https://tds.fandom.com)",
						Required:    true,
					},
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "username",
						Description: "Your Fandom username on that wiki",
						Required:    true,
					},
				},
			},
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "refresh",
				Description: "Refresh your widget data",
			},
		},
		IntegrationTypes: new([]discordgo.ApplicationIntegrationType{discordgo.ApplicationIntegrationUserInstall}),
		Contexts: new([]discordgo.InteractionContextType{
			discordgo.InteractionContextGuild,
			discordgo.InteractionContextBotDM,
			discordgo.InteractionContextPrivateChannel,
		}),
	},
}

func (b *Bot) onReady(s *discordgo.Session, _ *discordgo.Ready) {
	if _, err := s.ApplicationCommandBulkOverwrite(b.appID, "", widgetCommands); err != nil {
		log.Println("register commands:", err)
		return
	}
	log.Println("registered commands")
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		data := i.ApplicationCommandData()
		if data.Name != "widget" || len(data.Options) == 0 {
			return
		}
		switch data.Options[0].Name {
		case "setup":
			b.handleSetup(s, i, data.Options[0].Options)
		case "refresh":
			b.handleRefresh(s, i)
		}
	case discordgo.InteractionMessageComponent:
		if strings.HasPrefix(i.MessageComponentData().CustomID, "verify:") {
			b.handleVerify(s, i)
		}
	}
}

func (b *Bot) handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate, opts []*discordgo.ApplicationCommandInteractionDataOption) {
	wiki := opts[0].StringValue()
	username := opts[1].StringValue()

	wikiSub, err := parseWiki(wiki)
	if err != nil {
		respond(s, i, err.Error(), true)
		return
	}

	fandomUserID, err := b.fandom.LookupUserID(wiki, username)
	if err != nil {
		respond(s, i, err.Error(), true)
		return
	}

	uid := discordUserID(i)
	token := verificationToken(b.token, uid)
	customID := fmt.Sprintf("verify:%s:%d", wikiSub, fandomUserID)

	profileURL := fmt.Sprintf("https://%s.fandom.com/wiki/User:%s", wikiSub, urlPathEscape(username))

	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label: "Profile",
				Style: discordgo.LinkButton,
				URL:   profileURL,
			},
			discordgo.Button{
				Label:    "Verify",
				Style:    discordgo.PrimaryButton,
				CustomID: customID,
			},
		}},
	}

	respondComponents(s, i, fmt.Sprintf(
		"Add this to your Fandom profile bio, then click **Verify**:\n`%s`",
		token,
	), components, true)
}

func (b *Bot) handleVerify(s *discordgo.Session, i *discordgo.InteractionCreate) {
	parts := strings.SplitN(i.MessageComponentData().CustomID, ":", 3)
	if len(parts) != 3 {
		respond(s, i, "Invalid verify button.", true)
		return
	}
	wiki, userIDStr := parts[1], parts[2]

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		return
	}

	var fandomUserID int64
	fmt.Sscan(userIDStr, &fandomUserID)

	profile, err := b.fandom.GetProfile(wiki, fandomUserID)
	if err != nil {
		followup(s, i, err.Error())
		return
	}

	uid := discordUserID(i)
	expected := verificationToken(b.token, uid)
	if !strings.Contains(profile.Bio, expected) {
		followup(s, i, "Verification failed. Put the exact token in your Fandom profile bio and try again.")
		return
	}

	if err := b.store.Save(UserLink{
		DiscordID: uid,
		Wiki:      wiki,
		Username:  profile.Username,
		UserID:    fandomUserID,
	}); err != nil {
		followup(s, i, "Failed to save link.")
		return
	}

	wikiInfo, err := b.fandom.GetWikiInfo(wiki)
	if err != nil {
		followup(s, i, err.Error())
		return
	}

	if err := syncWidget(b.appID, b.token, uid, wikiInfo, profile); err != nil {
		b.followupSyncError(s, i, "Linked, but widget sync failed: ", err)
		return
	}

	followup(s, i, "Verified and synced! Widget auto-refreshes every 30 minutes.")
}

func (b *Bot) handleRefresh(s *discordgo.Session, i *discordgo.InteractionCreate) {
	uid := discordUserID(i)
	link, err := b.store.Get(uid)
	if err != nil {
		respond(s, i, "Run `/widget setup` first.", true)
		return
	}

	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		return
	}

	wikiInfo, err := b.fandom.GetWikiInfo(link.Wiki)
	if err != nil {
		followup(s, i, err.Error())
		return
	}

	if err := b.syncLink(link, wikiInfo); err != nil {
		b.followupSyncError(s, i, "Sync failed: ", err)
		return
	}
	followup(s, i, "Widget refreshed!")
}

func discordUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return i.User.ID
}

func verificationToken(secret, discordID string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(discordID))
	return "fandomwidget-" + hex.EncodeToString(m.Sum(nil))
}

func urlPathEscape(s string) string {
	return strings.ReplaceAll(s, " ", "_")
}

func respond(s *discordgo.Session, i *discordgo.InteractionCreate, content string, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: flags},
	})
}

func respondComponents(s *discordgo.Session, i *discordgo.InteractionCreate, content string, components []discordgo.MessageComponent, ephemeral bool) {
	flags := discordgo.MessageFlags(0)
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: components,
			Flags:      flags,
		},
	})
}

func followup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Flags:   discordgo.MessageFlagsEphemeral,
	})
}

func oauthAuthorizeComponents(appID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label: "Authorize",
				Style: discordgo.LinkButton,
				URL:   widgetAuthURL(appID),
			},
		}},
	}
}

func (b *Bot) followupSyncError(s *discordgo.Session, i *discordgo.InteractionCreate, prefix string, err error) {
	log.Println("sync:", err)
	if errors.Is(err, ErrOAuthRequired) {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content:    prefix + "click **Authorize**, then run `/widget refresh`.",
			Components: oauthAuthorizeComponents(b.appID),
			Flags:      discordgo.MessageFlagsEphemeral,
		})
		return
	}
	followup(s, i, prefix+err.Error())
}
