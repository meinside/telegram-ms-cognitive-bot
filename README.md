# telegram-ms-cognitive-bot

This Telegram Bot was built for showing how to use [Go library for MS Cognitive Services](https://github.com/meinside/ms-cognitive-services-go).

## Preparation

Install essential libraries and packages:

```bash
# for freetype font and image manipulation
$ sudo apt-get install libgl1-mesa-dev
$ go get github.com/golang/freetype/...
$ go get github.com/llgcode/draw2d/...
$ go get github.com/disintegration/gift

# for telegram bot api
$ go get github.com/meinside/telegram-bot-go

# for ms cognitive services
$ go get github.com/meinside/ms-cognitive-services-go/...

# for loggly
$ go get github.com/meinside/loggly-go
```

## Install & Build

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

## How to Run as a Service

### a. systemd

```bash
$ sudo cp systemd/telegram-ms-cognitive-bot.service /lib/systemd/system/
$ sudo vi /lib/systemd/system/telegram-ms-cognitive-bot.service
```

and edit **User**, **Group**, **WorkingDirectory** and **ExecStart** values.

It will launch automatically on boot with:

```bash
$ sudo systemctl enable telegram-ms-cognitive-bot.service
```

and will start with:

```bash
$ sudo systemctl start telegram-ms-cognitive-bot.service
```

## License

MIT

