package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	// for drawing rects and numbers on images
	"bytes"
	"github.com/golang/freetype"
	"github.com/golang/freetype/truetype"
	"github.com/llgcode/draw2d/draw2dimg"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"
	"net/http"

	// for MS Cognitive Services
	cog "github.com/meinside/ms-cognitive-services-go"
	cv "github.com/meinside/ms-cognitive-services-go/client/computervision"
	emotion "github.com/meinside/ms-cognitive-services-go/client/emotion"
	face "github.com/meinside/ms-cognitive-services-go/client/face"

	// for Telegram bot
	bot "github.com/meinside/telegram-bot-go"

	// for logging on Loggly
	"github.com/meinside/loggly-go"
)

var client *bot.Bot = nil
var logger *loggly.Loggly = nil

const (
	AppName = "MSCognitiveServicesBot"
)

type LogglyLog struct {
	Application string      `json:"app"`
	Severity    string      `json:"severity"`
	Message     string      `json:"message,omitempty"`
	Object      interface{} `json:"obj,omitempty"`
}

type CognitiveCommand string

// XXX - First letter of commands should be unique.
const (
	Emotion     CognitiveCommand = "Emotion Recognition"
	Face        CognitiveCommand = "Face Detection"
	Describe    CognitiveCommand = "Describe This Image"
	Ocr         CognitiveCommand = "OCR"
	Handwritten CognitiveCommand = "Handwritten Text Recognition"
	Tag         CognitiveCommand = "Tag This Image"
)

// XXX - When a new command is added, add it here too.
var allCmds = []CognitiveCommand{
	Emotion,
	Face,
	Describe,
	Ocr,
	Handwritten,
	Tag,
}
var shortCmdsMap = map[CognitiveCommand]string{}
var cmdsMap = map[string]CognitiveCommand{}

var emotionClient *emotion.Client
var cvClient *cv.Client
var faceClient *face.Client

var font *truetype.Font

var colors = []color.RGBA{
	color.RGBA{255, 255, 0, 255}, // yellow
	color.RGBA{0, 255, 255, 255}, // cyan
	color.RGBA{255, 0, 255, 255}, // purple
	color.RGBA{0, 255, 0, 255},   // green
	color.RGBA{0, 0, 255, 255},   // blue
	color.RGBA{255, 0, 0, 255},   // red
}

const (
	MessageActionImage     = "Choose action for this image:"
	MessageUnprocessable   = "Unprocessable message."
	MessageFailedToGetFile = "Failed to get file from the server."
	MessageCanceled        = "Canceled."
	MessageHelp            = `Send any image to this bot, and select one of the following actions:

- Emotion Recognition
- Face Detection
- Describe This Image
- OCR
- Handwritten Text Recognition
- Tag This Image

then it will send the result message or image back to you.
`

	CommandCancel = "cancel"

	FontFilepath = "fonts/RobotoCondensed-Regular.ttf"
	CircleRadius = 6
	StrokeWidth  = 7.0
)

const (
	ConfigFilename = "config.json"
)

type Config struct {
	TelegramApiToken                string `json:"telegram-api-token"`
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
	if file, err := ioutil.ReadFile(ConfigFilename); err != nil {
		panic(err)
	} else {
		if err := json.Unmarshal(file, &conf); err != nil {
			panic(err)
		}
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
	client = bot.NewClient(conf.TelegramApiToken)
	client.Verbose = conf.IsVerbose

	// loggly
	if conf.LogglyToken != "" {
		logger = loggly.New(conf.LogglyToken)
	}

	// others
	if bytes, err := ioutil.ReadFile(FontFilepath); err == nil {
		if f, err := truetype.Parse(bytes); err == nil {
			font = f
		} else {
			panic(err)
		}
	} else {
		panic(err)
	}
}

func genImageInlineKeyboards(fileId string) [][]bot.InlineKeyboardButton {
	data := map[string]string{}
	for _, cmd := range []CognitiveCommand{Emotion, Face, Describe, Ocr, Handwritten, Tag} {
		data[string(cmd)] = fmt.Sprintf("%s%s", shortCmdsMap[cmd], fileId)
	}

	return append(bot.NewInlineKeyboardButtonsAsRowsWithCallbackData(data), []bot.InlineKeyboardButton{
		bot.InlineKeyboardButton{Text: strings.Title(CommandCancel), CallbackData: CommandCancel},
	})
}

// process incoming update from Telegram
func processUpdate(b *bot.Bot, update bot.Update) bool {
	result := false // process result

	var message string
	var options map[string]interface{} = map[string]interface{}{}

	if update.Message.HasPhoto() {
		lastIndex := len(update.Message.Photo) - 1 // XXX - last one is the largest

		options["reply_markup"] = bot.InlineKeyboardMarkup{
			InlineKeyboard: genImageInlineKeyboards(*update.Message.Photo[lastIndex].FileId),
		}
		message = MessageActionImage
	} else if update.Message.HasDocument() && strings.HasPrefix(*update.Message.Document.MimeType, "image/") {
		options["reply_markup"] = bot.InlineKeyboardMarkup{
			InlineKeyboard: genImageInlineKeyboards(*update.Message.Document.FileId),
		}
		message = MessageActionImage
	} else {
		message = MessageHelp
	}

	// send message
	if sent := b.SendMessage(update.Message.Chat.Id, &message, options); sent.Ok {
		result = true
	} else {
		logError(fmt.Sprintf("Failed to send message: %s", *sent.Description))
	}

	return result
}

// process incoming callback query
func processCallbackQuery(b *bot.Bot, update bot.Update) bool {
	// process result
	result := false

	var username string
	message := ""
	query := *update.CallbackQuery
	data := *query.Data

	if data == CommandCancel {
		message = MessageCanceled
	} else {
		command := cmdsMap[string(data[0])]
		fileId := string(data[1:])

		if fileResult := b.GetFile(&fileId); fileResult.Ok {
			fileUrl := b.GetFileUrl(*fileResult.Result)

			if strings.Contains(*query.Message.Text, "image") {
				go processImage(b, query.Message.Chat.Id, fileUrl, command)

				message = fmt.Sprintf("Processing '%s' on received image...", command)

				// log request
				if query.From.Username == nil {
					username = *query.From.FirstName
				} else {
					username = *query.From.Username
				}
				logRequest(username, fileUrl, command)
			} else {
				message = MessageUnprocessable
			}
		} else {
			logError(fmt.Sprintf("Failed to get file from url: %s", *fileResult.Description))

			message = MessageFailedToGetFile
		}
	}

	// answer callback query
	if apiResult := b.AnswerCallbackQuery(query.Id, nil); apiResult.Ok {
		// edit message and remove inline keyboards
		options := map[string]interface{}{
			"chat_id":    query.Message.Chat.Id,
			"message_id": query.Message.MessageId,
		}
		if apiResult := b.EditMessageText(&message, options); apiResult.Ok {
			result = true
		} else {
			logError(fmt.Sprintf("Failed to edit message text: %s", *apiResult.Description))
		}
	} else {
		logError(fmt.Sprintf("Failed to answer callback query: %+v", query))
	}

	return result
}

// process requested image processing
func processImage(b *bot.Bot, chatId int64, fileUrl string, command CognitiveCommand) {
	message := ""
	errorMessage := ""

	// 'typing...'
	b.SendChatAction(chatId, bot.ChatActionTyping)

	switch command {
	case Emotion:
		// send a photo (draw squares on detected faces) and emotions in text
		if emotions, err := emotionClient.RecognizeImage(fileUrl, nil); err == nil {
			if len(emotions) > 0 {
				// open image from url,
				if resp, err := http.Get(fileUrl); err == nil {
					defer resp.Body.Close()
					if img, _, err := image.Decode(resp.Body); err == nil {
						var rect cog.Rectangle
						var emos []string
						var scores []string

						// copy to a new image
						newImg := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
						draw.Draw(newImg, newImg.Bounds(), img, image.ZP, draw.Src)
						gc := draw2dimg.NewGraphicContext(newImg)
						gc.SetLineWidth(StrokeWidth)
						gc.SetFillColor(color.Transparent)

						// prepare freetype font
						fc := freetype.NewContext()
						fc.SetFont(font)
						fc.SetDPI(72)
						fc.SetClip(newImg.Bounds())
						fc.SetDst(newImg)
						fontSize := float64(newImg.Bounds().Dy()) / 24.0
						fc.SetFontSize(fontSize)

						for i, e := range emotions {
							rect = e.FaceRectangle

							// set color
							color := colorForIndex(i)
							gc.SetStrokeColor(color)
							fc.SetSrc(&image.Uniform{color})

							// draw rectangles and their indices on detected faces
							gc.MoveTo(float64(rect.Left), float64(rect.Top))
							gc.LineTo(float64(rect.Left+rect.Width), float64(rect.Top))
							gc.LineTo(float64(rect.Left+rect.Width), float64(rect.Top+rect.Height))
							gc.LineTo(float64(rect.Left), float64(rect.Top+rect.Height))
							gc.LineTo(float64(rect.Left), float64(rect.Top))
							gc.Close()
							gc.FillStroke()

							// draw face label
							if _, err = fc.DrawString(
								fmt.Sprintf("Face #%d", i+1),
								freetype.Pt(
									rect.Left,
									int(fc.PointToFixed(float64(rect.Top+rect.Height)+fontSize)>>6),
								),
							); err != nil {
								logError(fmt.Sprintf("Failed to draw string: %s", err))
							}

							// emotion string
							for k, v := range e.Scores {
								scores = append(scores, fmt.Sprintf("  %s: %.3f%%", k, v*100.0))
							}
							emos = append(emos, strings.Join(scores, "\n"))
						}
						gc.Save()

						// build up emotions string
						var strs []string
						for i, e := range emos {
							strs = append(strs, fmt.Sprintf("[Face #%d]\n%s", i+1, e))
						}
						message = fmt.Sprintf("%s", strings.Join(strs, "\n\n"))

						// 'uploading photo...'
						b.SendChatAction(chatId, bot.ChatActionUploadPhoto)

						// send a photo with rectangles drawn on detected faces
						buf := new(bytes.Buffer)
						if err := jpeg.Encode(buf, newImg, nil); err == nil {
							if sent := b.SendPhotoWithBytes(chatId, buf.Bytes(), nil); sent.Ok {
								// send emotions string
								if sent := b.SendMessage(chatId, &message, nil); !sent.Ok {
									errorMessage = fmt.Sprintf("Failed to send emotions: %s", *sent.Description)
								}
							} else {
								errorMessage = fmt.Sprintf("Failed to send a marked image: %s", *sent.Description)
							}
						}
					} else {
						errorMessage = fmt.Sprintf("Failed to decode image: %s", err)
					}
				} else {
					errorMessage = fmt.Sprintf("Failed to open image: %s", err)
				}
			} else {
				errorMessage = "No emotion recognized. Given image may be too small or low-quality."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to recognize emotion: %s", err)
		}
	case Face:
		if faces, err := faceClient.Detect(fileUrl, true, true, []string{"age", "gender", "headPose", "smile", "facialHair", "glasses", "emotion"}); err == nil {
			if len(faces) > 0 {
				// open image from url,
				if resp, err := http.Get(fileUrl); err == nil {
					defer resp.Body.Close()
					if img, _, err := image.Decode(resp.Body); err == nil {
						var rect cog.Rectangle

						// copy to a new image
						newImg := image.NewRGBA(image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()))
						draw.Draw(newImg, newImg.Bounds(), img, image.ZP, draw.Src)
						gc := draw2dimg.NewGraphicContext(newImg)
						gc.SetLineWidth(StrokeWidth)
						gc.SetFillColor(color.Transparent)

						// prepare freetype font
						fc := freetype.NewContext()
						fc.SetFont(font)
						fc.SetDPI(72)
						fc.SetClip(newImg.Bounds())
						fc.SetDst(newImg)
						fontSize := float64(newImg.Bounds().Dy()) / 24.0
						fc.SetFontSize(fontSize)

						// build up facial attributes string
						strs := []string{}
						var facialHairs, headPoses, emotions []string
						for i, f := range faces {
							rect = f.FaceRectangle

							// set color
							color := colorForIndex(i)
							gc.SetStrokeColor(color)
							fc.SetSrc(&image.Uniform{color})

							// draw rectangles and their indices on detected faces
							gc.MoveTo(float64(rect.Left), float64(rect.Top))
							gc.LineTo(float64(rect.Left+rect.Width), float64(rect.Top))
							gc.LineTo(float64(rect.Left+rect.Width), float64(rect.Top+rect.Height))
							gc.LineTo(float64(rect.Left), float64(rect.Top+rect.Height))
							gc.LineTo(float64(rect.Left), float64(rect.Top))
							gc.Close()
							gc.FillStroke()

							// draw face label
							if _, err = fc.DrawString(
								fmt.Sprintf("Face #%d", i+1),
								freetype.Pt(
									rect.Left,
									int(fc.PointToFixed(float64(rect.Top+rect.Height)+fontSize)>>6),
								),
							); err != nil {
								logError(fmt.Sprintf("Failed to draw string: %s", err))
							}

							// mark face landmarks
							if p, exists := f.FaceLandmarks["noseTip"]; exists { // nose tip
								gc.MoveTo(float64(p.X), float64(p.Y))
								gc.ArcTo(float64(p.X), float64(p.Y), CircleRadius, CircleRadius, 0, -math.Pi*2)
								gc.Close()
								gc.FillStroke()
							}
							if p, exists := f.FaceLandmarks["pupilRight"]; exists { // right pupil
								gc.MoveTo(float64(p.X), float64(p.Y))
								gc.ArcTo(float64(p.X), float64(p.Y), CircleRadius, CircleRadius, 0, -math.Pi*2)
								gc.Close()
								gc.FillStroke()
							}
							if p, exists := f.FaceLandmarks["pupilLeft"]; exists { // left pupil
								gc.MoveTo(float64(p.X), float64(p.Y))
								gc.ArcTo(float64(p.X), float64(p.Y), CircleRadius, CircleRadius, 0, -math.Pi*2)
								gc.Close()
								gc.FillStroke()
							}
							if p1, exists := f.FaceLandmarks["mouthRight"]; exists { // mouth
								if p2, exists := f.FaceLandmarks["mouthLeft"]; exists {
									gc.MoveTo(float64(p1.X), float64(p1.Y))
									gc.LineTo(float64(p2.X), float64(p2.Y))
									gc.Close()
									gc.FillStroke()
								}
							}

							facialHairs = []string{}
							for k, v := range f.FaceAttributes.FacialHair {
								facialHairs = append(facialHairs, fmt.Sprintf("  %s: %.3f%%", k, v*100.0))
							}
							headPoses = []string{}
							for k, v := range f.FaceAttributes.HeadPose {
								headPoses = append(headPoses, fmt.Sprintf("  %s: %.2f°", k, v))
							}
							emotions = []string{}
							for k, v := range f.FaceAttributes.Emotion {
								emotions = append(emotions, fmt.Sprintf("  %s: %.3f%%", k, v*100.0))
							}

							strs = append(strs, fmt.Sprintf("[Face #%d]\n> Facial Hair\n%s\n> Head Pose\n%s\n> Emotion\n%s", i+1, strings.Join(facialHairs, "\n"), strings.Join(headPoses, "\n"), strings.Join(emotions, "\n")))
						}
						gc.Save()
						message = fmt.Sprintf("%s", strings.Join(strs, "\n\n"))

						// 'uploading photo...'
						b.SendChatAction(chatId, bot.ChatActionUploadPhoto)

						// send a photo with rectangles drawn on detected faces
						buf := new(bytes.Buffer)
						if err := jpeg.Encode(buf, newImg, nil); err == nil {
							if sent := b.SendPhotoWithBytes(chatId, buf.Bytes(), nil); sent.Ok {
								// send face attributes string
								if sent := b.SendMessage(chatId, &message, nil); !sent.Ok {
									errorMessage = fmt.Sprintf("Failed to send face attributes: %s", *sent.Description)
								}
							} else {
								errorMessage = fmt.Sprintf("Failed to send a face-marked image: %s", *sent.Description)
							}
						}
					} else {
						errorMessage = fmt.Sprintf("Failed to decode image: %s", err)
					}
				} else {
					errorMessage = fmt.Sprintf("Failed to open image: %s", err)
				}
			} else {
				errorMessage = "No face detected. Given image may be too small or low-quality."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to detect faces: %s", err)
		}
	case Describe:
		if described, err := cvClient.DescribeImage(fileUrl, 0); err == nil {
			captions := []string{}
			for _, c := range described.Description.Captions {
				captions = append(captions, fmt.Sprintf("%s (%.3f%%)", c.Text, c.Confidence*100.0))
			}
			message = fmt.Sprintf("%s\n\n(%s)", strings.Join(captions, "\n"), strings.Join(described.Description.Tags, ", "))

			if len(strings.TrimSpace(message)) > 0 {
				// send described text
				if sent := b.SendMessage(chatId, &message, nil); !sent.Ok {
					errorMessage = fmt.Sprintf("Failed to send described text: %s", *sent.Description)
				}
			} else {
				errorMessage = "Could not describe given image. It may be too small or low-quality."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to describe image: %s", err)
		}
	case Ocr:
		if recognized, err := cvClient.Ocr(fileUrl, "unk", true); err == nil {
			words := []string{}
			for _, r := range recognized.Regions {
				for _, l := range r.Lines {
					for _, w := range l.Words {
						words = append(words, w.Text)
					}
				}
			}
			message = fmt.Sprintf("%s\n", strings.Join(words, " "))

			if len(strings.TrimSpace(message)) > 0 {
				// send detected text
				if sent := b.SendMessage(chatId, &message, nil); !sent.Ok {
					errorMessage = fmt.Sprintf("Failed to send recognized text: %s", *sent.Description)
				}
			} else {
				errorMessage = "Could not recognize any text from given image. It may be too small or low-quality."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to recognize text: %s", err)
		}
	case Handwritten:
		if recognized, err := cvClient.RecognizeHandwritten(fileUrl, true, nil); err == nil {
			words := []string{}
			for _, l := range recognized.Lines {
				words = append(words, l.Text)
			}
			message = fmt.Sprintf("%s", strings.Join(words, " "))

			if len(strings.TrimSpace(message)) > 0 {
				// send detected text
				if sent := b.SendMessage(chatId, &message, nil); !sent.Ok {
					errorMessage = fmt.Sprintf("Failed to send recognized text: %s", *sent.Description)
				}
			} else {
				errorMessage = "Could not recognize any text from given image. It may be too small or low-quality."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to recognize handwritten text: %s", err)
		}
	case Tag:
		if recognized, err := cvClient.TagImage(fileUrl); err == nil {
			tags := []string{}
			for _, t := range recognized.Tags {
				tags = append(tags, fmt.Sprintf("%s (%.3f%%)", t.Name, t.Confidence*100.0))
			}
			message = strings.Join(tags, "\n")

			if len(strings.TrimSpace(message)) > 0 {
				// send tags
				if sent := b.SendMessage(chatId, &message, nil); !sent.Ok {
					errorMessage = fmt.Sprintf("Failed to send tags: %s", *sent.Description)
				}
			} else {
				errorMessage = "Could not tag given image. It may be too small or low-quality."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to tag image: %s", err)
		}
	}

	if errorMessage != "" {
		b.SendMessage(chatId, &errorMessage, nil)

		logError(errorMessage)
	}
}

// rotate color
func colorForIndex(i int) color.RGBA {
	length := len(colors)
	return colors[i%length]
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
		logMessage(fmt.Sprintf("Starting bot: @%s (%s)", *me.Result.Username, *me.Result.FirstName))

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

func logMessage(message string) {
	log.Println(message)

	if logger != nil {
		logger.Log(LogglyLog{
			Application: AppName,
			Severity:    "Log",
			Message:     message,
		})
	}
}

func logError(message string) {
	log.Println(message)

	if logger != nil {
		logger.Log(LogglyLog{
			Application: AppName,
			Severity:    "Error",
			Message:     message,
		})
	}
}

func logRequest(username, fileUrl string, command CognitiveCommand) {
	if logger != nil {
		logger.Log(LogglyLog{
			Application: AppName,
			Severity:    "Verbose",
			Object: struct {
				Username string           `json:"username"`
				FileUrl  string           `json:"file_url"`
				Command  CognitiveCommand `json:"command"`
			}{
				Username: username,
				FileUrl:  fileUrl,
				Command:  command,
			},
		})
	}
}
