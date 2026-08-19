package app

import (
	"testing"

	"github.com/google/uuid"
)

func TestValidateWebhookForBill(t *testing.T) {
	accountID := uuid.New()
	channelID := uuid.New()
	channel := channelRecord{
		ID:        channelID,
		AccountID: accountID,
		Provider:  "alipay",
		Config: providerConfig{Alipay: &alipayConfig{
			PID:   "seller-001",
			AppID: "app-001",
		}},
	}
	bill := billRecord{
		AccountID:       accountID,
		ChannelID:       channelID,
		Provider:        "alipay",
		MerchantOrderNo: "ORDER-001",
		AmountMinor:     1099,
		Currency:        "CNY",
	}
	valid := webhookResult{
		MerchantOrderNo: "ORDER-001",
		MerchantID:      "seller-001",
		AppID:           "app-001",
		AmountMinor:     1099,
		Currency:        "CNY",
	}

	tests := []struct {
		name   string
		mutate func(*channelRecord, *billRecord, *webhookResult)
		wantOK bool
	}{
		{name: "accepts exact binding", mutate: func(*channelRecord, *billRecord, *webhookResult) {}, wantOK: true},
		{name: "rejects different channel", mutate: func(_ *channelRecord, bill *billRecord, _ *webhookResult) { bill.ChannelID = uuid.New() }},
		{name: "rejects different provider", mutate: func(_ *channelRecord, bill *billRecord, _ *webhookResult) { bill.Provider = "wechat" }},
		{name: "rejects different amount", mutate: func(_ *channelRecord, _ *billRecord, result *webhookResult) { result.AmountMinor = 1 }},
		{name: "rejects different currency", mutate: func(_ *channelRecord, _ *billRecord, result *webhookResult) { result.Currency = "USD" }},
		{name: "rejects different merchant", mutate: func(_ *channelRecord, _ *billRecord, result *webhookResult) { result.MerchantID = "seller-002" }},
		{name: "rejects different app", mutate: func(_ *channelRecord, _ *billRecord, result *webhookResult) { result.AppID = "app-002" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotChannel, gotBill, gotResult := channel, bill, valid
			test.mutate(&gotChannel, &gotBill, &gotResult)
			err := validateWebhookForBill(gotChannel, gotBill, gotResult)
			if (err == nil) != test.wantOK {
				t.Fatalf("validateWebhookForBill() error = %v, want success=%v", err, test.wantOK)
			}
		})
	}
}

func TestWechatWebhookAmount(t *testing.T) {
	amount, currency, err := wechatWebhookAmount(map[string]any{"total": float64(1099), "currency": "cny"})
	if err != nil || amount != 1099 || currency != "CNY" {
		t.Fatalf("valid webhook amount = (%d, %q, %v)", amount, currency, err)
	}

	for _, value := range []any{
		map[string]any{"total": float64(10.5), "currency": "CNY"},
		map[string]any{"total": float64(-1), "currency": "CNY"},
		map[string]any{"total": float64(1)},
		map[string]any{"currency": "CNY"},
		nil,
	} {
		if _, _, err := wechatWebhookAmount(value); err == nil {
			t.Fatalf("wechatWebhookAmount(%#v) unexpectedly succeeded", value)
		}
	}
}
