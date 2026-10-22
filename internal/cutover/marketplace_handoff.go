package cutover

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const maxCutoverTTL = 300

type DNSRecords interface {
	ZoneID(ctx context.Context, domain string) (string, error)
	UpsertCNAME(ctx context.Context, zoneID, name, content string, ttl int, idempotencyKey string) error
}

type SellerAsset struct {
	Domain         string `json:"domain"`
	RecordName     string `json:"record_name"`
	PreviousTarget string `json:"previous_target"`
	NewTarget      string `json:"new_target"`
}

type BuyerUpdate struct {
	BuyerID string `json:"buyer_id"`
	Message string `json:"message"`
}

type OrderHandoff struct {
	OrderID string `json:"order_id"`
	State   string `json:"state"`
}

type Request struct {
	CutoverID   string       `json:"cutover_id"`
	TTL         int          `json:"ttl"`
	SellerAsset SellerAsset  `json:"seller_asset"`
	BuyerUpdate BuyerUpdate  `json:"buyer_update"`
	Order       OrderHandoff `json:"order_handoff"`
}

type Result struct {
	CutoverID    string       `json:"cutover_id"`
	DNSState     string       `json:"dns_state"`
	ActiveTarget string       `json:"active_target"`
	TTL          int          `json:"ttl"`
	BuyerUpdate  BuyerUpdate  `json:"buyer_update"`
	Order        OrderHandoff `json:"order_handoff"`
}

type Workflow struct {
	dns      DNSRecords
	mu       sync.RWMutex
	requests map[string]Request
}

func NewWorkflow(dns DNSRecords) *Workflow {
	return &Workflow{dns: dns, requests: make(map[string]Request)}
}

func (w *Workflow) CutOver(ctx context.Context, input Request) (Result, error) {
	if err := validate(input); err != nil {
		return Result{}, err
	}
	zoneID, err := w.dns.ZoneID(ctx, input.SellerAsset.Domain)
	if err != nil {
		return Result{}, err
	}
	if err := w.dns.UpsertCNAME(ctx, zoneID, input.SellerAsset.RecordName, input.SellerAsset.NewTarget, input.TTL, input.CutoverID+":apply"); err != nil {
		return Result{}, err
	}

	w.mu.Lock()
	w.requests[input.CutoverID] = input
	w.mu.Unlock()
	return resultFor(input, input.SellerAsset.NewTarget, "cutover_active", "ready_for_buyer"), nil
}

func (w *Workflow) RollBack(ctx context.Context, cutoverID string) (Result, error) {
	w.mu.RLock()
	input, found := w.requests[cutoverID]
	w.mu.RUnlock()
	if !found {
		return Result{}, errors.New("cutover_id was not found")
	}
	zoneID, err := w.dns.ZoneID(ctx, input.SellerAsset.Domain)
	if err != nil {
		return Result{}, err
	}
	if err := w.dns.UpsertCNAME(ctx, zoneID, input.SellerAsset.RecordName, input.SellerAsset.PreviousTarget, input.TTL, input.CutoverID+":rollback"); err != nil {
		return Result{}, err
	}
	return resultFor(input, input.SellerAsset.PreviousTarget, "rolled_back", "handoff_paused"), nil
}

func validate(input Request) error {
	if strings.TrimSpace(input.CutoverID) == "" || strings.TrimSpace(input.SellerAsset.Domain) == "" || strings.TrimSpace(input.SellerAsset.RecordName) == "" {
		return errors.New("cutover_id, domain, and record_name are required")
	}
	if input.SellerAsset.PreviousTarget == "" || input.SellerAsset.NewTarget == "" {
		return errors.New("previous_target and new_target are required")
	}
	if input.BuyerUpdate.BuyerID == "" || input.Order.OrderID == "" {
		return errors.New("buyer_id and order_id are required")
	}
	if input.TTL < 30 || input.TTL > maxCutoverTTL {
		return fmt.Errorf("ttl must be between 30 and %d seconds", maxCutoverTTL)
	}
	return nil
}

func resultFor(input Request, target, dnsState, orderState string) Result {
	input.Order.State = orderState
	return Result{
		CutoverID:    input.CutoverID,
		DNSState:     dnsState,
		ActiveTarget: target,
		TTL:          input.TTL,
		BuyerUpdate:  input.BuyerUpdate,
		Order:        input.Order,
	}
}
