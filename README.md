# fandomwidget

Discord profile widget bot for Fandom wiki stats.

<img width="557" height="377" alt="example" src="https://github.com/user-attachments/assets/a2e957ba-67ea-48b0-88dd-1ac8d6689790" />

## Setup

1. Create a [Discord application](https://discord.com/developers/applications), enable Social SDK then create a widget, field names samples are in `widget.example.json`.
2. Copy `config.example.json` to `config.json` and set `discord_token`, you may want to set this through an environment variable as well.
3. Run the bot (`go run .` or the release binary).
4. Install the bot to your account, run `/widget setup`, verify via Fandom profile bio.

Auto-syncs every 30 minutes. Use `/widget refresh` to force a sync.

## License

[MIT](LICENSE)
