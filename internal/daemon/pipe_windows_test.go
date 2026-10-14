package daemon

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The pipe's SDDL, parsed by Windows: the user owns it, and its DACL is
// protected and lets in the user alone.
func TestThePipeSecurityDescriptorLetsInTheUserAlone(t *testing.T) {
	me, err := userSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString(pipeSecurityDescriptor(me.String()))
	if err != nil {
		t.Fatalf("Windows refuses the SDDL: %v", err)
	}
	owner, _, err := sd.Owner()
	if err != nil || !owner.Equals(me) {
		t.Errorf("owner %v, %v; want %v", owner, err, me)
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Errorf("the DACL is not protected (control %#x, %v): it would inherit entries", control, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("no DACL: %v", err)
	}
	if dacl.AceCount != 1 {
		t.Fatalf("the DACL has %d entries, want the user's alone", dacl.AceCount)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || !sid.Equals(me) {
		t.Errorf("the entry is type %d for %v; want access allowed for %v", ace.Header.AceType, sid, me)
	}
}
