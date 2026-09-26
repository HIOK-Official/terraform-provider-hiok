package provider

import "testing"

// InternalValidate catches schema mistakes the compiler cannot — a Computed field
// also marked Required, a Default on a Required field, missing Elem on a list, and so
// on. Without it those only surface when a user runs terraform against the provider.
func TestProviderSchemaIsValid(t *testing.T) {
	if err := New().InternalValidate(); err != nil {
		t.Fatalf("provider schema is invalid: %s", err)
	}
}

// Every resource this provider registers must be usable: it needs a create, a read,
// and a delete, or practitioners get a resource that cannot complete a lifecycle.
func TestEveryResourceHasFullLifecycle(t *testing.T) {
	for name, resource := range New().ResourcesMap {
		if resource.CreateContext == nil {
			t.Errorf("%s has no create function", name)
		}
		if resource.ReadContext == nil {
			t.Errorf("%s has no read function", name)
		}
		if resource.DeleteContext == nil {
			t.Errorf("%s has no delete function", name)
		}
	}
}
