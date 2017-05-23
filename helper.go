package main

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"

	// for manipulating images
	"github.com/disintegration/gift"
	"github.com/golang/freetype"
	"github.com/llgcode/draw2d/draw2dimg"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math"

	// for MS Cognitive Services
	cog "github.com/meinside/ms-cognitive-services-go"

	// for Telegram bot
	bot "github.com/meinside/telegram-bot-go"
)

const (
	CircleRadius = 6
	StrokeWidth  = 7.0
)

// colors
var colors = []color.RGBA{
	color.RGBA{255, 255, 0, 255}, // yellow
	color.RGBA{0, 255, 255, 255}, // cyan
	color.RGBA{255, 0, 255, 255}, // purple
	color.RGBA{0, 255, 0, 255},   // green
	color.RGBA{0, 0, 255, 255},   // blue
	color.RGBA{255, 0, 0, 255},   // red
}
var maskColor = color.RGBA{0, 0, 0, 255} // black

// process incoming update from Telegram
func processUpdate(b *bot.Bot, update bot.Update) bool {
	result := false // process result

	var message string
	var options = map[string]interface{}{
		"reply_to_message_id": update.Message.MessageId,
	}

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
				go processImage(b, query.Message.Chat.Id, query.Message.MessageId, fileUrl, command)

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
func processImage(b *bot.Bot, chatId int64, messageIdToDelete int, fileUrl string, command CognitiveCommand) {
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
							var scores []string
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
							strs = append(strs,
								fmt.Sprintf(`[Face #%d]
%s`,
									i+1,
									e,
								),
							)
						}
						message = fmt.Sprintf("%s", strings.Join(strs, "\n\n"))

						// 'uploading photo...'
						b.SendChatAction(chatId, bot.ChatActionUploadPhoto)

						// send a photo with rectangles drawn on detected faces
						buf := new(bytes.Buffer)
						if err := jpeg.Encode(buf, newImg, nil); err == nil {
							if sent := b.SendPhotoWithBytes(chatId, buf.Bytes(), map[string]interface{}{
								"caption": fmt.Sprintf("Process result of '%s'", command),
							}); sent.Ok {
								// send emotions string
								if sent := b.SendMessage(chatId, &message, map[string]interface{}{
									"reply_to_message_id": sent.Result.MessageId,
								}); !sent.Ok {
									errorMessage = fmt.Sprintf("Failed to send emotions: %s", *sent.Description)
								}
							} else {
								errorMessage = fmt.Sprintf("Failed to send image: %s", *sent.Description)
							}
						}
					} else {
						errorMessage = fmt.Sprintf("Failed to decode image: %s", err)
					}
				} else {
					errorMessage = fmt.Sprintf("Failed to open image: %s", err)
				}
			} else {
				errorMessage = "No emotion recognized on this image."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to recognize emotion: %s", err)
		}
	case Face, CensorEyes, MaskFaces:
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

						// build up facial attributes string
						strs := []string{}
						var facialHairs, headPoses, emotions []string
						for i, f := range faces {
							switch command {
							case Face:
								// prepare freetype font
								fc := freetype.NewContext()
								fc.SetFont(font)
								fc.SetDPI(72)
								fc.SetClip(newImg.Bounds())
								fc.SetDst(newImg)
								fontSize := float64(newImg.Bounds().Dy()) / 24.0
								fc.SetFontSize(fontSize)

								// set color
								color := colorForIndex(i)
								gc.SetStrokeColor(color)
								fc.SetSrc(&image.Uniform{color})

								// draw rectangles and their indices on detected faces
								rect = f.FaceRectangle
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
								if hasAllKeys([]string{
									"noseTip",
									"pupilRight",
									"pupilLeft",
									"mouthRight",
									"mouthLeft",
								}, f.FaceLandmarks) {
									// nose tip
									n, _ := f.FaceLandmarks["noseTip"]
									gc.MoveTo(float64(n.X), float64(n.Y))
									gc.ArcTo(float64(n.X), float64(n.Y), CircleRadius, CircleRadius, 0, -math.Pi*2)
									gc.Close()
									gc.FillStroke()

									// right pupil
									r, _ := f.FaceLandmarks["pupilRight"]
									gc.MoveTo(float64(r.X), float64(r.Y))
									gc.ArcTo(float64(r.X), float64(r.Y), CircleRadius, CircleRadius, 0, -math.Pi*2)
									gc.Close()
									gc.FillStroke()

									// left pupil
									l, _ := f.FaceLandmarks["pupilLeft"]
									gc.MoveTo(float64(l.X), float64(l.Y))
									gc.ArcTo(float64(l.X), float64(l.Y), CircleRadius, CircleRadius, 0, -math.Pi*2)
									gc.Close()
									gc.FillStroke()

									// mouth
									m1, _ := f.FaceLandmarks["mouthRight"]
									m2, _ := f.FaceLandmarks["mouthLeft"]
									gc.MoveTo(float64(m1.X), float64(m1.Y))
									gc.LineTo(float64(m2.X), float64(m2.Y))
									gc.Close()
									gc.FillStroke()
								}

								// descriptions
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

								strs = append(strs,
									fmt.Sprintf(`[Face #%d]
> Facial Hair
%s
> Head Pose
%s
> Emotion
%s`,
										i+1,
										strings.Join(facialHairs, "\n"),
										strings.Join(headPoses, "\n"),
										strings.Join(emotions, "\n"),
									),
								)
							case CensorEyes:
								if hasAllKeys([]string{
									"eyeLeftTop",
									"eyeLeftBottom",
									"eyeLeftOuter",
									"eyeRightTop",
									"eyeRightBottom",
									"eyeRightOuter",
								}, f.FaceLandmarks) {
									// points
									lt, _ := f.FaceLandmarks["eyeLeftTop"]
									lb, _ := f.FaceLandmarks["eyeLeftBottom"]
									lo, _ := f.FaceLandmarks["eyeLeftOuter"]
									rt, _ := f.FaceLandmarks["eyeRightTop"]
									rb, _ := f.FaceLandmarks["eyeRightBottom"]
									ro, _ := f.FaceLandmarks["eyeRightOuter"]

									// mask size
									w := math.Abs(float64(lo.X) - float64(ro.X))
									h := math.Abs(math.Min(float64(lt.Y), float64(rt.Y)) - math.Max(float64(lb.Y), float64(rb.Y)))
									marginX, marginY := w*0.2, h*0.3

									// set mask color
									gc.SetFillColor(maskColor)

									// fill mask rectangle
									gc.MoveTo(float64(lo.X)-marginX, math.Min(float64(lt.Y), float64(rt.Y))-marginY)
									gc.LineTo(float64(ro.X)+marginX, math.Min(float64(lt.Y), float64(rt.Y))-marginY)
									gc.LineTo(float64(ro.X)+marginX, math.Max(float64(lb.Y), float64(rb.Y))+marginY)
									gc.LineTo(float64(lo.X)-marginX, math.Max(float64(lb.Y), float64(rb.Y))+marginY)
									gc.LineTo(float64(lo.X)-marginX, math.Min(float64(lt.Y), float64(rt.Y))-marginY)
									gc.Close()
									gc.Fill()
								}
							case MaskFaces:
								rect = f.FaceRectangle

								// pixelate face rects
								g := gift.New(
									gift.Pixelate(rect.Width / 8),
								)
								g.DrawAt(
									newImg,
									newImg.SubImage(image.Rect(rect.Left, rect.Top, rect.Left+rect.Width, rect.Top+rect.Height)),
									image.Pt(rect.Left, rect.Top),
									gift.CopyOperator,
								)
							}
						}
						gc.Save()

						// build up message
						switch command {
						case Face:
							message = strings.Join(strs, "\n\n")
						default:
							message = ""
						}

						// 'uploading photo...'
						b.SendChatAction(chatId, bot.ChatActionUploadPhoto)

						// send a photo with rectangles drawn on detected faces
						buf := new(bytes.Buffer)
						if err := jpeg.Encode(buf, newImg, nil); err == nil {
							if sent := b.SendPhotoWithBytes(chatId, buf.Bytes(), map[string]interface{}{
								"caption": fmt.Sprintf("Process result of '%s'", command),
							}); sent.Ok {
								// reply to
								var replyTo map[string]interface{} = nil
								if command == Face {
									replyTo = map[string]interface{}{
										"reply_to_message_id": sent.Result.MessageId,
									}
								}

								// send result string
								if len(message) > 0 {
									if sent := b.SendMessage(chatId, &message, replyTo); !sent.Ok {
										errorMessage = fmt.Sprintf("Failed to send faces: %s", *sent.Description)
									}
								}
							} else {
								errorMessage = fmt.Sprintf("Failed to send image: %s", *sent.Description)
							}
						} else {
							errorMessage = fmt.Sprintf("Failed to encode image: %s", err)
						}
					} else {
						errorMessage = fmt.Sprintf("Failed to decode image: %s", err)
					}
				} else {
					errorMessage = fmt.Sprintf("Failed to open image: %s", err)
				}
			} else {
				errorMessage = "No face detected on this image."
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
				errorMessage = "Could not describe given image."
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
				errorMessage = "Could not recognize any text from given image."
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
				errorMessage = "Could not recognize any text from given image."
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
				errorMessage = "Could not tag given image."
			}
		} else {
			errorMessage = fmt.Sprintf("Failed to tag image: %s", err)
		}
	default:
		errorMessage = fmt.Sprintf("Command not supported: %s", command)
	}

	// delete original message
	b.DeleteMessage(chatId, messageIdToDelete)

	// if there was any error, send it back
	if errorMessage != "" {
		b.SendMessage(chatId, &errorMessage, nil)

		logError(errorMessage)
	}
}

// generate inline keyboards for selecting action
func genImageInlineKeyboards(fileId string) [][]bot.InlineKeyboardButton {
	data := map[string]string{}
	for _, cmd := range allCmds {
		data[string(cmd)] = fmt.Sprintf("%s%s", shortCmdsMap[cmd], fileId)
	}

	return append(bot.NewInlineKeyboardButtonsAsRowsWithCallbackData(data), []bot.InlineKeyboardButton{
		bot.InlineKeyboardButton{Text: strings.Title(CommandCancel), CallbackData: CommandCancel},
	})
}

// rotate color
func colorForIndex(i int) color.RGBA {
	length := len(colors)
	return colors[i%length]
}

// check if give face landmarks has all requsted keys
func hasAllKeys(keys []string, points map[string]cog.Point) bool {
	for _, k := range keys {
		if _, exists := points[k]; !exists {
			return false
		}
	}

	return true
}
