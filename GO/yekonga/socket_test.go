package yekonga

import (
	"encoding/json"
	"testing"

	"github.com/robertkonga/yekonga-server-go/datatype"
	"github.com/robertkonga/yekonga-server-go/plugins/mongo-driver/bson"
)

func newTestSocketClient(n *Namespace, id string, tenantId interface{}) *Client {
	req := &Request{Context: datatype.Context{}}
	if tenantId != nil {
		req.SetTenantId(tenantId)
	}

	c := &Client{ID: id, send: make(chan []byte, 4), Namespace: n, Request: req}
	n.Clients[id] = c

	return c
}

func received(c *Client) []byte {
	select {
	case msg := <-c.send:
		return msg
	default:
		return nil
	}
}

func TestBroadcastToTenant(t *testing.T) {
	n := &Namespace{Clients: map[string]*Client{}, Rooms: map[string]map[string]bool{}}
	tenantA := bson.NewObjectID()

	a := newTestSocketClient(n, "a", tenantA)            // ObjectID, as the middleware sets it
	a2 := newTestSocketClient(n, "a2", tenantA.Hex())    // same tenant as a string
	b := newTestSocketClient(n, "b", bson.NewObjectID()) // another tenant
	admin := newTestSocketClient(n, "admin", nil)        // no tenant

	n.broadcastToTenant(tenantA.Hex(), "database", datatype.DataMap{"action": "create", "model": "Order"})

	msgA, msgA2, msgB, msgAdmin := received(a), received(a2), received(b), received(admin)
	if msgA == nil || msgA2 == nil || msgAdmin == nil {
		t.Fatalf("tenant and admin clients should receive the event: a=%v a2=%v admin=%v", msgA != nil, msgA2 != nil, msgAdmin != nil)
	}
	if msgB != nil {
		t.Fatal("another tenant's client received the event")
	}
	if &msgA[0] != &msgAdmin[0] {
		t.Error("the event was encoded separately for each client")
	}

	var event struct {
		Event string
		Data  map[string]string
	}
	if err := json.Unmarshal(msgA, &event); err != nil || event.Event != "database" || event.Data["model"] != "Order" {
		t.Errorf("message = %s (%v)", msgA, err)
	}

	// No tenant: everyone.
	n.broadcastToTenant("", "database", datatype.DataMap{"action": "create", "model": "Setting"})
	for _, c := range []*Client{a, a2, b, admin} {
		if received(c) == nil {
			t.Errorf("client %s missed an event without a tenant", c.ID)
		}
	}
}

func TestBroadcastExcludesSender(t *testing.T) {
	n := &Namespace{Clients: map[string]*Client{}, Rooms: map[string]map[string]bool{}}
	sender := newTestSocketClient(n, "sender", nil)
	other := newTestSocketClient(n, "other", nil)

	n.Broadcast("chat", "hi", sender)

	if received(sender) != nil {
		t.Error("the sender received its own broadcast")
	}
	if received(other) == nil {
		t.Error("another client missed the broadcast")
	}
}
