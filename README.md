# telegram-ms-cognitive-bot

This Telegram Bot was built for showing how to use [Go library for MS Cognitive Services](https://github.com/meinside/ms-cognitive-services-go).

## How to Install & Build

```bash
$ git clone https://github.com/meinside/telegram-ms-cognitive-bot.git
$ cd telegram-ms-cognitive-bot/
$ go build
```

## How to Configure

Copy the sample config file and fill it with your values:

```bash
$ cp config.json.sample config.json
$ vi config.json
```

For example:

```json
{
	"telegram-api-token": "0123456789:AaBbCcDdEeFfGgHhIiJj_klmnopqrstuvwx-yz",
	"telegram-monitor-interval-seconds": 1,
	"ms-emotion-subscription-key": "abcdefghijklmnopqrstuvwxyz0123456789",
	"ms-computervision-subscription-key": "0123456789abcdefghijklmnopqrstuvwxyz",
	"ms-face-subscription-key": "01234abcdefghijklmnopqrstuvwxyz56789",
	"is-verbose": false
}
```

## How to Run

After all things are setup correctly, just run the built binary:

```bash
$ ./telegram-ms-cognitive-bot
```

## License

MIT

