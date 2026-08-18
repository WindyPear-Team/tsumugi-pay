package app

import (
	"context"
	"time"
)

const billQueryPollInterval = 5 * time.Second

// StartBillPolling reconciles pending bill-query payments without relying on a
// browser remaining open on the checkout page.
func (s *Service) StartBillPolling(ctx context.Context) {
	go func() {
		s.pollPendingBillQueryOrders(ctx)

		ticker := time.NewTicker(billQueryPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.pollPendingBillQueryOrders(ctx)
			}
		}
	}()
}

func (s *Service) pollPendingBillQueryOrders(ctx context.Context) {
	var pending []Bill
	if err := s.db.DB().WithContext(ctx).
		Where("status = ? AND provider = ? AND (expires_at IS NULL OR expires_at > ?)", "pending", "alipay", nowUTC()).
		Order("created_at ASC").
		Limit(100).
		Find(&pending).Error; err != nil {
		s.logger.Warn("load pending bill-query payments failed", "error", err)
		return
	}

	for _, model := range pending {
		if ctx.Err() != nil {
			return
		}
		if !s.shouldPollBill(model.ID) {
			continue
		}
		channel, err := s.channelByID(ctx, model.ChannelID)
		if err != nil {
			s.logger.Warn("load bill-query channel failed", "bill_id", model.ID, "error", err)
			continue
		}
		if channel.Provider != "alipay" || !channel.Enabled || alipayPaymentMode(channel.Config.Alipay) != "bill_query" {
			continue
		}
		if _, _, err := s.pollBill(ctx, billRecordFromModel(model)); err != nil {
			s.logger.Warn("background payment polling failed", "bill_id", model.ID, "error", err)
		}
	}
}
