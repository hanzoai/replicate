package replicate

import "testing"

func TestSyncPolicy_String(t *testing.T) {
	cases := []struct {
		p    SyncPolicy
		want string
	}{
		{SyncAll, "all"},
		{SyncQuorumW2, "quorum_w2"},
		{SyncLocalOnly, "local_only"},
		{SyncPolicy(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.p.String(); got != c.want {
			t.Errorf("SyncPolicy(%d).String() = %q want %q", c.p, got, c.want)
		}
	}
}
