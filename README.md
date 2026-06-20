# telegram_server

Telegram bot server written in Go using [telego](https://github.com/mymmrac/telego).

## Requirements

- Go 1.26+
- A Telegram bot token from [@BotFather](https://t.me/BotFather)

## Run

```sh
$env:TELEGRAM_BOT_TOKEN = "your-token-here"   # PowerShell
go run .
```

## Build

```sh
go build -o telegram_server.exe .
```
