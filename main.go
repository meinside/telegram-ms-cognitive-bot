package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"syscall"

	// for using .ttf
	"github.com/golang/freetype/truetype"

	// MS Cognitive Services clients
	cv "github.com/meinside/ms-cognitive-services-go/client/computervision"
	emotion "github.com/meinside/ms-cognitive-services-go/client/emotion"
	face "github.com/meinside/ms-cognitive-services-go/client/face"

	// for Telegram bot
	bot "github.com/meinside/telegram-bot-go"

	// for logging on Loggly
	"github.com/meinside/loggly-go"
)

var client *bot.Bot
var logger *loggly.Loggly

const (
	appName = "MSCognitiveServicesBot"
)

// LogglyLog struct
type LogglyLog struct {
	Application string      `json:"app"`
	Severity    string      `json:"severity"`
	Message     string      `json:"message,omitempty"`
	Object      interface{} `json:"obj,omitempty"`
}

// CognitiveCommand type
type CognitiveCommand string

// XXX - First letter of commands should be unique.
const (
	Emotion     CognitiveCommand = "Emotion Recognition"
	Face        CognitiveCommand = "Face Detection"
	Describe    CognitiveCommand = "Describe This Image"
	Ocr         CognitiveCommand = "OCR"
	Handwritten CognitiveCommand = "Handwritten Text Recognition"
	Tag         CognitiveCommand = "Tag This Image"

	// fun commands
	CensorEyes CognitiveCommand = "Censor Eyes"
	MaskFaces  CognitiveCommand = "Mask Faces"
)

// XXX - When a new command is added, add it here too.
var allCmds = []CognitiveCommand{
	Emotion,
	Face,
	Describe,
	Ocr,
	Handwritten,
	Tag,

	// fun commands
	CensorEyes,
	MaskFaces,
}
var shortCmdsMap = map[CognitiveCommand]string{}
var cmdsMap = map[string]CognitiveCommand{}

var emotionClient *emotion.Client
var cvClient *cv.Client
var faceClient *face.Client

var font *truetype.Font

const (
	messageActionImage     = "Choose action for this image:"
	messageUnprocessable   = "Unprocessable message."
	messageFailedToGetFile = "Failed to get file from the server."
	messageCanceled        = "Canceled."
	messageHelp            = `Send any image to this bot, and select one of the following actions:

- Emotion Recognition
- Face Detection
- Describe This Image
- OCR
- Handwritten Text Recognition
- Tag This Image
- Censor Eyes
- Mask Faces

then it will send the result message and/or image back to you.

* Github: https://github.com/meinside/telegram-ms-cognitive-bot
`

	commandCancel = "cancel"

	fontFilepath = "fonts/RobotoCondensed-Regular.ttf"
)

const (
	configFilename = "config.json"
)

// Config struct
type Config struct {
	TelegramAPIToken                string `json:"telegram-api-token"`
	TelegramMonitorIntervalSeconds  int    `json:"telegram-monitor-interval-seconds"`
	MsEmotionSubscriptionKey        string `json:"ms-emotion-subscription-key"`
	MsComputervisionSubscriptionKey string `json:"ms-computervision-subscription-key"`
	MsFaceSubscriptionKey           string `json:"ms-face-subscription-key"`
	LogglyToken                     string `json:"loggly-token,omitempty"`
	IsVerbose                       bool   `json:"is-verbose"`
}

var conf Config

func init() {
	// read from config file
	if file, err := ioutil.ReadFile(configFilename); err != nil {
		panic(err)
	} else {
		if err := json.Unmarshal(file, &conf); err != nil {
			panic(err)
		}
	}

	// check values
	if conf.TelegramMonitorIntervalSeconds <= 0 {
		conf.TelegramMonitorIntervalSeconds = 1
	}

	// ms cognitive services
	emotionClient = emotion.NewClient(conf.MsEmotionSubscriptionKey)
	cvClient = cv.NewClient(conf.MsComputervisionSubscriptionKey)
	faceClient = face.NewClient(conf.MsFaceSubscriptionKey)
	var firstLetter string
	for _, c := range allCmds {
		firstLetter = string(string(c)[0])

		shortCmdsMap[c] = firstLetter
		cmdsMap[firstLetter] = c
	}

	// telegram
	client = bot.NewClient(conf.TelegramAPIToken)
	client.Verbose = conf.IsVerbose

	// loggly
	if conf.LogglyToken != "" {
		logger = loggly.New(conf.LogglyToken)
	}

	// others
	if bytes, err := ioutil.ReadFile(fontFilepath); err == nil {
		if f, err := truetype.Parse(bytes); err == nil {
			font = f
		} else {
			panic(err)
		}
	} else {
		panic(err)
	}
}

func main() {
	// catch SIGINT and SIGTERM and terminate gracefully
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sig
		os.Exit(1)
	}()

	// get info about this bot
	if me := client.GetMe(); me.Ok {
		logMessage(fmt.Sprintf("Starting bot: @%s (%s)", *me.Result.Username, me.Result.FirstName))

		// delete webhook (getting updates will not work when wehbook is set up)
		if unhooked := client.DeleteWebhook(); unhooked.Ok {
			// wait for new updates
			client.StartMonitoringUpdates(
				0,
				conf.TelegramMonitorIntervalSeconds,
				func(b *bot.Bot, update bot.Update, err error) {
					if err == nil {
						if update.HasMessage() {
							processUpdate(b, update) // process message
						} else if update.HasCallbackQuery() {
							processCallbackQuery(b, update) // process callback query
						} else {
							logError("Update not processable")
						}
					} else {
						logError(fmt.Sprintf("Error while receiving update (%s)", err))
					}
				},
			)
		} else {
			panic("Failed to delete webhook")
		}
	} else {
		panic("Failed to get info of the bot")
	}
}

// log message
func logMessage(message string) {
	log.Println(message)

	if logger != nil {
		logger.Log(LogglyLog{
			Application: appName,
			Severity:    "Log",
			Message:     message,
		})
	}
}

// log error message
func logError(message string) {
	log.Println(message)

	if logger != nil {
		logger.Log(LogglyLog{
			Application: appName,
			Severity:    "Error",
			Message:     message,
		})
	}
}

// log request from user
func logRequest(username, fileURL string, command CognitiveCommand) {
	if logger != nil {
		logger.Log(LogglyLog{
			Application: appName,
			Severity:    "Verbose",
			Object: struct {
				Username string           `json:"username"`
				FileURL  string           `json:"file_url"`
				Command  CognitiveCommand `json:"command"`
			}{
				Username: username,
				FileURL:  fileURL,
				Command:  command,
			},
		})
	}
}
