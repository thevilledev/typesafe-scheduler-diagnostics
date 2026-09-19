/*
Copyright 2026 The TypeSafe Scheduler Diagnostics Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package observation

import "testing"

func TestMemoryBoundsAndOrdersObservations(t *testing.T) {
	store := NewMemory(2)
	store.Record(Observation{ID: "old"})
	store.Record(Observation{ID: "middle"})
	store.Record(Observation{ID: "new"})

	items := store.List(10)
	if got, want := len(items), 2; got != want {
		t.Fatalf("item count = %d, want %d", got, want)
	}
	if got, want := items[0].ID, "new"; got != want {
		t.Errorf("first item = %q, want %q", got, want)
	}
	if got, want := items[1].ID, "middle"; got != want {
		t.Errorf("second item = %q, want %q", got, want)
	}

	store.Clear()
	if got := len(store.List(10)); got != 0 {
		t.Errorf("item count after Clear = %d, want 0", got)
	}
}

func TestLifecycleUpdatesReplaceAndNotifyWithoutBlocking(t *testing.T) {
	store := NewMemory(2)
	changed, unsubscribe := store.Subscribe()
	for i := 0; i < 1000; i++ {
		store.Record(Observation{ID: "request", Stage: "queued"})
	}
	select {
	case <-changed:
	default:
		t.Fatal("observer was not notified")
	}
	store.Record(Observation{ID: "request", Stage: "evaluating"})
	items := store.List(10)
	if len(items) != 1 || items[0].Stage != "evaluating" {
		t.Fatalf("lifecycle update created duplicate or stale observations: %#v", items)
	}
	<-changed
	unsubscribe()
	store.Record(Observation{ID: "request", Stage: "complete"})
	select {
	case <-changed:
		t.Fatal("unsubscribed observer was notified")
	default:
	}
}
