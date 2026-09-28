package yekonga

import (
	"sync"
	"testing"
	"time"
)

// A trigger that registers another trigger needs the write lock. That used to
// deadlock because the dispatcher held the read lock while the trigger ran.
func TestTriggerCallbackDoesNotHoldLockWhileRunning(t *testing.T) {
	y := &YekongaData{}

	y.setTrigger("Order", BeforeFindTriggerAction, nil, nil, func(ctx *RequestContext, q *QueryContext) (interface{}, error) {
		y.setTrigger("Order", AfterFindTriggerAction, nil, nil, func(*RequestContext, *QueryContext) (interface{}, error) {
			return nil, nil
		})
		return "ran", nil
	})

	done := make(chan interface{}, 1)
	go func() {
		result, _ := y.triggerCallback("Order", BeforeFindTriggerAction, nil, &QueryContext{})
		done <- result
	}()

	select {
	case result := <-done:
		if result != "ran" {
			t.Fatalf("trigger result = %v, want %q", result, "ran")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("triggerCallback deadlocked")
	}
}

func TestTriggerCallbackLookup(t *testing.T) {
	y := &YekongaData{}
	y.setTrigger("Order", BeforeCreateTriggerAction, "admin", "list", func(*RequestContext, *QueryContext) (interface{}, error) {
		return "admin_list", nil
	})

	if result, err := y.triggerCallback("Order", BeforeCreateTriggerAction, nil, &QueryContext{AccessRole: "admin", Route: "list"}); err != nil || result != "admin_list" {
		t.Errorf("registered trigger: got (%v, %v)", result, err)
	}

	if _, err := y.triggerCallback("Missing", BeforeCreateTriggerAction, nil, &QueryContext{}); err == nil {
		t.Error("unknown model: expected an error")
	}

	if _, err := y.triggerCallback("Order", AfterCreateTriggerAction, nil, &QueryContext{}); err == nil {
		t.Error("unknown action: expected an error")
	}

	// A role with no trigger reports false, which callers treat as "don't proceed".
	if result, err := y.triggerCallback("Order", BeforeCreateTriggerAction, nil, &QueryContext{AccessRole: "guest"}); err == nil || result != false {
		t.Errorf("unregistered role: got (%v, %v), want (false, error)", result, err)
	}
}

func TestTriggerCallbackConcurrent(t *testing.T) {
	y := &YekongaData{}
	y.setTriggerAll(BeforeFindTriggerAllAction, func(*DataModel, *RequestContext, *QueryContext) (interface{}, error) {
		return nil, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			y.triggerAllCallback(BeforeFindTriggerAllAction, nil, nil, &QueryContext{})
		}()
		go func() {
			defer wg.Done()
			y.setTrigger("Order", BeforeFindTriggerAction, nil, nil, func(*RequestContext, *QueryContext) (interface{}, error) {
				return nil, nil
			})
		}()
	}
	wg.Wait()
}
