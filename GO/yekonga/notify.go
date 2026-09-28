package yekonga

import (
	"fmt"

	"github.com/robertkonga/yekonga-server-go/helper"
	"github.com/robertkonga/yekonga-server-go/helper/console"
)

// NotifiedUser represents the user data structure
type NotifiedUser struct {
	UserID   string `json:"userId"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
	Whatsapp string `json:"whatsapp"`
}

// NotificationParams holds the parameters for the notification
type NotificationParams struct {
	Title         string
	HTML          string
	Text          string
	Whatsapp      string
	SenderName    *string
	ReplyTo       *string
	Name          *string
	Link          *string
	Filename      *string
	ProfileID     *string
	TenantID      *string
	ModuleName    string
	ReferenceID   *string
	ReferenceName *string
}

// NotificationBody represents the body for the notification request
type NotificationBody struct {
	ID             string  `json:"id,omitempty"`
	NotificationID string  `json:"notificationId,omitempty"`
	ProfileID      *string `json:"profileId,omitempty"`
	UserID         string  `json:"userId"`
	ReferenceID    *string `json:"referenceId,omitempty"`
	ReferenceName  *string `json:"referenceName,omitempty"`
	Recipient      string  `json:"recipient"`
	RecipientName  *string `json:"recipientName,omitempty"`
	ReplyTo        *string `json:"replyTo,omitempty"`
	Title          string  `json:"title"`
	Link           *string `json:"link,omitempty"`
	Attachment     *string `json:"attachment,omitempty"`
	IsSeen         bool    `json:"isSeen"`
	SenderName     *string `json:"senderName,omitempty"`
	Status         string  `json:"status"`
	Timestamp      string  `json:"timestamp"`
	Type           string  `json:"type"`
	Content        string  `json:"content"`
}

func (y *YekongaData) Notify(user *NotifiedUser, params NotificationParams) error {
	if user == nil {
		return fmt.Errorf("user is nil")
	}

	modelName := "Notification"
	console.Warn("user", user, params.Text)

	// Common notification body
	body := NotificationBody{
		ProfileID:     params.ProfileID,
		UserID:        user.UserID,
		ReferenceID:   params.ReferenceID,
		ReferenceName: params.ReferenceName,
		RecipientName: params.Name,
		ReplyTo:       params.ReplyTo,
		Title:         params.Title,
		Link:          params.Link,
		Attachment:    params.Filename,
		IsSeen:        false,
		SenderName:    params.SenderName,
		Status:        "waiting",
		Timestamp:     helper.GetTimestamp(nil).String(),
	}

	// Email notification
	if helper.IsNotEmpty(params.HTML) && helper.IsEmail(user.Email) {
		emailContact := user.Email
		if helper.IsEmail(emailContact) {
			mailBody := body
			mailBody.Recipient = emailContact
			mailBody.Type = "mail"
			mailBody.ID = helper.GetHexString(24)
			mailBody.Content = helper.TextTemplate(params.HTML, map[string]interface{}{
				"notificationId": mailBody.ID,
			}, nil)

			// jsonBody, err := json.Marshal(mailBody)
			// if err != nil {
			// 	return fmt.Errorf("error marshaling email body: %v", err)
			// }

			_ = y.ModelQuery(modelName).Create(helper.ToMap[interface{}](mailBody))
		}
	}

	// SMS notification
	if helper.IsNotEmpty(params.Text) && helper.IsPhone(user.Phone) {
		phoneContact := helper.FormatPhone(user.Phone)
		if helper.IsPhone(phoneContact) {
			smsBody := body
			smsBody.Content = params.Text
			smsBody.Recipient = phoneContact
			smsBody.Type = "sms"

			// jsonBody, err := json.Marshal(smsBody)
			// if err != nil {
			// 	return fmt.Errorf("error marshaling sms body: %v", err)
			// }

			_ = y.ModelQuery(modelName).Create(helper.ToMap[interface{}](smsBody))
		}
	}

	// WhatsApp notification
	if helper.IsNotEmpty(params.Whatsapp) && helper.IsPhone(user.Whatsapp) {
		whatsappContact := helper.FormatPhone(user.Whatsapp)
		if helper.IsPhone(whatsappContact) {
			whatsappBody := body
			whatsappBody.Content = params.Whatsapp
			whatsappBody.Recipient = whatsappContact
			whatsappBody.Type = "whatsapp"

			// jsonBody, err := json.Marshal(whatsappBody)
			// if err != nil {
			// 	return fmt.Errorf("error marshaling whatsapp body: %v", err)
			// }

			_ = y.ModelQuery(modelName).Create(helper.ToMap[interface{}](whatsappBody))
		}
	}

	return nil
}
