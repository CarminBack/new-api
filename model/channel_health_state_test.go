package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChannelHealthStateLifecycle(t *testing.T) {
	truncateTables(t)
	state := &ChannelHealthState{
		ScopeKey:    "29:fingerprint:route:test",
		ChannelID:   29,
		Fingerprint: "fingerprint",
		Scope:       "route",
		ModelName:   "gpt-test",
		RequestPath: "/v1/responses",
		State:       "probing",
		NextProbeAt: 100,
		Revision:    2,
		ProbeID:     7,
		ProbeType:   "initial",
	}
	require.NoError(t, SaveChannelHealthState(state))

	states, err := ListChannelHealthStates()
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, state.ScopeKey, states[0].ScopeKey)
	require.Equal(t, uint64(2), states[0].Revision)

	state.State = "open"
	state.NextProbeAt = 200
	state.Revision++
	require.NoError(t, SaveChannelHealthState(state))
	states, err = ListChannelHealthStates()
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, "open", states[0].State)
	require.Equal(t, int64(200), states[0].NextProbeAt)

	require.NoError(t, DeleteChannelHealthStatesForChannel(29))
	states, err = ListChannelHealthStates()
	require.NoError(t, err)
	require.Empty(t, states)
}
