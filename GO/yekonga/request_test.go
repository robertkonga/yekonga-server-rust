package yekonga

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestDatabaseContextCancelledOnDisconnect(t *testing.T) {
	httpCtx, disconnect := context.WithCancel(context.Background())
	req := &Request{HttpRequest: httptest.NewRequest("GET", "/", nil).WithContext(httpCtx)}

	stop := req.watchDisconnect()
	defer stop()

	disconnect() // the client goes away while the handler runs

	select {
	case <-req.DatabaseContext().Done():
	case <-time.After(time.Second):
		t.Fatal("database context not cancelled after the client disconnected")
	}
}

func TestDatabaseContextOutlivesHandler(t *testing.T) {
	httpCtx, finish := context.WithCancel(context.Background())
	req := &Request{HttpRequest: httptest.NewRequest("GET", "/", nil).WithContext(httpCtx)}

	stop := req.watchDisconnect()
	stop()   // the handler returns...
	finish() // ...then net/http cancels the request context

	time.Sleep(10 * time.Millisecond)
	if err := req.DatabaseContext().Err(); err != nil {
		t.Fatalf("database context cancelled after the handler returned: %v", err)
	}
}

func TestDatabaseContextWithoutRequest(t *testing.T) {
	var req *Request
	if req.DatabaseContext() == nil {
		t.Fatal("nil request gave a nil context")
	}

	if (&Request{}).DatabaseContext() == nil {
		t.Fatal("request without a watcher gave a nil context")
	}
}

func TestModelIndexes(t *testing.T) {
	model := &DataModel{Fields: map[string]DataModelField{
		"id":       {Name: "id", Kind: DataModelID},
		"name":     {Name: "name"},
		"tenantId": {Name: "tenantId", Kind: DataModelID},
		"outletId": {Name: "outletId", Kind: DataModelID, ForeignKey: DataModelFieldForeignKey{ModelName: "Outlet"}},
		"code":     {Name: "code", Unique: true},
		"region":   {Name: "region", Index: true},
	}}

	want := []modelIndex{
		{field: "code", unique: true},
		{field: "outletId"},
		{field: "region"},
		{field: "tenantId"},
	}

	if got := modelIndexes(model); !reflect.DeepEqual(got, want) {
		t.Errorf("modelIndexes = %v, want %v", got, want)
	}
}
