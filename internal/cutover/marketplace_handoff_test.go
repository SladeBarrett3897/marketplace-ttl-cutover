package cutover

import (
	"context"
	"testing"
)

type fakeDNS struct {
	zoneID string
	writes []dnsWrite
}

type dnsWrite struct {
	zoneID, name, content, key string
	ttl                        int
}

func (f *fakeDNS) ZoneID(_ context.Context, domain string) (string, error) {
	return f.zoneID, nil
}

func (f *fakeDNS) UpsertCNAME(_ context.Context, zoneID, name, content string, ttl int, key string) error {
	f.writes = append(f.writes, dnsWrite{zoneID: zoneID, name: name, content: content, ttl: ttl, key: key})
	return nil
}

func TestMarketplaceCutoverDecision(t *testing.T) {
	tests := []struct {
		name       string
		ttl        int
		wantErr    bool
		wantTarget string
		wantState  string
		wantWrites int
	}{
		{name: "short TTL starts buyer handoff", ttl: 60, wantTarget: "new.market.example", wantState: "ready_for_buyer", wantWrites: 1},
		{name: "long TTL is rejected before DNS", ttl: 600, wantErr: true, wantWrites: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dns := &fakeDNS{zoneID: "zone_market_1"}
			workflow := NewWorkflow(dns)
			input := testRequest(tt.ttl)
			result, err := workflow.CutOver(context.Background(), input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CutOver() error = %v, wantErr %v", err, tt.wantErr)
			}
			if len(dns.writes) != tt.wantWrites {
				t.Fatalf("DNS writes = %d, want %d", len(dns.writes), tt.wantWrites)
			}
			if tt.wantErr {
				return
			}
			if result.ActiveTarget != tt.wantTarget || result.Order.State != tt.wantState {
				t.Fatalf("result target/state = %q/%q", result.ActiveTarget, result.Order.State)
			}
			if dns.writes[0].zoneID != "zone_market_1" || dns.writes[0].key != "cut-1042:apply" {
				t.Fatalf("write boundary = %#v", dns.writes[0])
			}
		})
	}
}

func TestRollbackRestoresSellerTarget(t *testing.T) {
	dns := &fakeDNS{zoneID: "zone_market_1"}
	workflow := NewWorkflow(dns)
	if _, err := workflow.CutOver(context.Background(), testRequest(60)); err != nil {
		t.Fatal(err)
	}
	result, err := workflow.RollBack(context.Background(), "cut-1042")
	if err != nil {
		t.Fatal(err)
	}
	if result.ActiveTarget != "old.market.example" || result.Order.State != "handoff_paused" {
		t.Fatalf("rollback result = %#v", result)
	}
	if got := dns.writes[1]; got.content != "old.market.example" || got.key != "cut-1042:rollback" {
		t.Fatalf("rollback write = %#v", got)
	}
}

func testRequest(ttl int) Request {
	return Request{
		CutoverID: "cut-1042",
		TTL:       ttl,
		SellerAsset: SellerAsset{
			Domain:         "example.com",
			RecordName:     "market.example.com",
			PreviousTarget: "old.market.example",
			NewTarget:      "new.market.example",
		},
		BuyerUpdate: BuyerUpdate{BuyerID: "buyer-88", Message: "Marketplace handoff is ready."},
		Order:       OrderHandoff{OrderID: "order-501"},
	}
}
