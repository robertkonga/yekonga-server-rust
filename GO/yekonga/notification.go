package yekonga

import (
	"time"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/gateway/setting"
	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
)

func (y *YekongaData) setNotification() {
	y.RegisterCronjob("SystemNotification", time.Second*10, func(app *YekongaData, time time.Time) {
		const notificationModelName = "Notification"

		where := map[string]interface{}{
			"status": map[string]interface{}{
				"equalTo": "waiting", // "submitted", "waiting", "delivered", "undelivered"
			},
		}
		list := app.ModelQuery(notificationModelName).WhereAll(where).Find(nil)
		count := len(*list)

		if count > 0 {
			console.Info("run notification", count)
		}

		for i := 0; i < count; i++ {
			note := (*list)[i]
			kind := helper.GetValueOfString(note, "type")
			noteId := helper.GetValueOfString(note, "_id")

			switch kind {
			case "sms":
				runNotificationSMS(note, app)
			case "mail":
				runNotificationMail(note, app)
			case "whatsapp":
				runNotificationWhatsapp(note, app)
			}

			app.ModelQuery(notificationModelName).Where("_id", noteId).Update(datatype.DataMap{
				"status": "submitted",
			}, nil)
		}
	})
}

func runNotificationSMS(note datatype.DataMap, app *YekongaData) {
	// console.Success("notification SMS", note)

	message := setting.SendParams{}
	phone := ""
	text := ""

	_phone := helper.GetValueOfString(note, "recipient")
	if helper.IsNotEmpty(_phone) {
		phone = _phone
	}

	_text := helper.GetValueOfString(note, "content")
	if helper.IsNotEmpty(_text) {
		text = _text
	}

	if helper.IsNotEmpty(phone) {
		message.Phone = phone
	}
	if helper.IsNotEmpty(text) {
		message.Text = text
	}

	app.SendSMS(message, nil)
}

func runNotificationMail(note datatype.DataMap, app *YekongaData) {
	// console.Success("notification EMAIL", note)

	valid := false
	to := []string{}
	cc := []string{}
	bcc := []string{}
	replyTo := ""
	subject := ""
	body := ""
	htmlBody := ""
	attachments := []helper.Attachment{}
	message := helper.EmailMessage{}

	_recipient := helper.GetValueOfString(note, "recipient")
	if helper.IsNotEmpty(_recipient) {
		to = append(to, _recipient)
	}

	_replyTo := helper.GetValueOfString(note, "replyTo")
	if helper.IsNotEmpty(_replyTo) {
		replyTo = _replyTo
	}

	_htmlBody := helper.GetValueOfString(note, "content")
	if helper.IsNotEmpty(_htmlBody) {
		htmlBody = _htmlBody
	}

	_subject := helper.GetValueOfString(note, "title")
	if helper.IsNotEmpty(_subject) {
		subject = _subject
	}

	_attachment := helper.GetValueOfString(note, "attachment")
	if helper.IsNotEmpty(_attachment) {
		file, err := message.GetAttachmentFromFile(_attachment)
		if err != nil {
			attachments = append(attachments, *file)
		}
	}

	if len(to) > 0 {
		message.To = to
	}
	if len(cc) > 0 {
		message.Cc = cc
	}
	if len(bcc) > 0 {
		message.Bcc = bcc
	}
	if helper.IsNotEmpty(replyTo) {
		message.ReplyTo = replyTo
	}
	if helper.IsNotEmpty(subject) {
		message.Subject = subject
	}
	if helper.IsNotEmpty(body) {
		message.Body = body
	}
	if helper.IsNotEmpty(htmlBody) {
		message.HTMLBody = htmlBody
	}
	if len(attachments) > 0 {
		message.Attachments = attachments
	}

	if len(to) > 0 && (helper.IsNotEmpty(body) || helper.IsNotEmpty(htmlBody)) {
		valid = true
	}

	if valid {
		app.SendEmail(message, nil)
	} else {
		console.Error("runNotificationMail", message)
	}
}

func runNotificationWhatsapp(note datatype.DataMap, app *YekongaData) {
	// console.Success("notification WHATSAPP", note)

	message := setting.SendParams{}
	phone := ""
	text := ""

	_phone := helper.GetValueOfString(note, "recipient")
	if helper.IsNotEmpty(_phone) {
		phone = _phone
	}

	_text := helper.GetValueOfString(note, "content")
	if helper.IsNotEmpty(_text) {
		text = _text
	}

	if helper.IsNotEmpty(phone) {
		message.Phone = phone
	}

	if helper.IsNotEmpty(text) {
		message.Text = text
	}

	content := helper.ToMap[any](text)
	if helper.IsNotEmpty(content) {
		message.Content = &setting.Content{
			Content: content,
		}
	}

	app.SendWhatsapp(message, nil)
}
