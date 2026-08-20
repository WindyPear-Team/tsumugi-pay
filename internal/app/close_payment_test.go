package app

import (
	"context"
	"testing"
)

func TestClosePaymentSkipsAlipayBillQueryProviderCall(t *testing.T) {
	service := &Service{}
	channel := channelRecord{
		Provider: "alipay",
		Config: providerConfig{Alipay: &alipayConfig{
			Mode: "bill_query",
		}},
	}

	if err := service.closePayment(context.Background(), channel, billRecord{MerchantOrderNo: "ORDER-CLOSE-001"}); err != nil {
		t.Fatalf("closePayment() error = %v, want local close for bill query mode", err)
	}
}
