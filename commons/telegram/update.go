package telegram

// Update is the minimal webhook payload shape consumed by relay and escalation handlers.
type Update struct {
	CallbackQuery *CallbackQuery   `json:"callback_query"`
	Message       *IncomingMessage `json:"message"`
}

// CallbackQuery is an inline-keyboard button press.
type CallbackQuery struct {
	ID   string `json:"id"`
	Data string `json:"data"`
}

// IncomingMessage is an operator text message (including replies).
type IncomingMessage struct {
	MessageID      int              `json:"message_id"`
	Text           string           `json:"text"`
	Chat           *Chat            `json:"chat"`
	ReplyToMessage *IncomingMessage `json:"reply_to_message"`
}

// Chat identifies the Telegram conversation that sent a message.
type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}
