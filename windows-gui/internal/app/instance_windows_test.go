//go:build windows

package app

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestNamedInstanceAllowsOnlyOneLease(t *testing.T) {
	name := fmt.Sprintf(`Local\AppleMusicDownloader.Test.%d.%d`, os.Getpid(), time.Now().UnixNano())
	releaseFirst, acquired, err := acquireNamedInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("first instance lease was not acquired")
	}
	defer releaseFirst()

	releaseSecond, acquired, err := acquireNamedInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if acquired || releaseSecond != nil {
		t.Fatal("second instance unexpectedly acquired the same lease")
	}

	releaseFirst()
	releaseThird, acquired, err := acquireNamedInstance(name)
	if err != nil {
		t.Fatal(err)
	}
	if !acquired {
		t.Fatal("instance lease was not available after release")
	}
	releaseThird()
}

func TestInstanceMutexNameIsStableAndUserScoped(t *testing.T) {
	first := instanceMutexName("S-1-5-21-100")
	if first != instanceMutexName("s-1-5-21-100") {
		t.Fatal("instance mutex name is sensitive to SID casing")
	}
	if first == instanceMutexName("S-1-5-21-200") {
		t.Fatal("different user SIDs produced the same instance mutex name")
	}
}
