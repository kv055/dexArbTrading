package telegram

// InlineButton is one Telegram inline-keyboard button.
type InlineButton struct {
	Text         string
	CallbackData string
}

// InlineKeyboard is a Telegram inline keyboard (rows of buttons).
type InlineKeyboard [][]InlineButton

// Row appends a keyboard row.
func (k *InlineKeyboard) Row(buttons ...InlineButton) {
	*k = append(*k, buttons)
}

// Button builds a button, truncating callback_data to Telegram's 64-byte limit.
func Button(text, callbackData string) InlineButton {
	if len(callbackData) > 64 {
		callbackData = callbackData[:64]
	}
	return InlineButton{Text: text, CallbackData: callbackData}
}

// Marshal returns the Telegram reply_markup body fragment.
func (k InlineKeyboard) Marshal() map[string]any {
	if len(k) == 0 {
		return nil
	}
	rows := make([][]map[string]string, 0, len(k))
	for _, row := range k {
		outRow := make([]map[string]string, 0, len(row))
		for _, b := range row {
			outRow = append(outRow, map[string]string{
				"text":          b.Text,
				"callback_data": b.CallbackData,
			})
		}
		rows = append(rows, outRow)
	}
	return map[string]any{"inline_keyboard": rows}
}
