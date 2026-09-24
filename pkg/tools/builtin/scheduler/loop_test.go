package scheduler

import (
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoopFiresAtDeadline(t *testing.T) {
	t.Parallel()

	for _, when := range []string{"in:1m", "every:1m"} {
		t.Run(when, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				ts := New()
				rt := &fakeRuntime{recall: true}
				res, err := ts.createSchedule(t.Context(), CreateScheduleArgs{Prompt: "check build", When: when}, rt)
				require.NoError(t, err)
				require.False(t, res.IsError, res.Output)
				require.NoError(t, ts.Start(t.Context()))
				t.Cleanup(func() { require.NoError(t, ts.Stop(t.Context())) })

				synctest.Sleep(time.Minute - time.Nanosecond)
				require.Empty(t, rt.messages())
				synctest.Sleep(time.Nanosecond)
				require.Len(t, rt.messages(), 1)
				require.Contains(t, rt.messages()[0], "check build")

				synctest.Sleep(time.Minute - time.Nanosecond)
				require.Len(t, rt.messages(), 1)
				synctest.Sleep(time.Nanosecond)
				if when == "every:1m" {
					require.Len(t, rt.messages(), 2)
					require.Len(t, ts.store.list(), 1)
				} else {
					require.Len(t, rt.messages(), 1)
					require.Empty(t, ts.store.list())
				}
			})
		})
	}
}

func TestLoopWakesForEarlierSchedule(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ts := New()
		rt := &fakeRuntime{recall: true}
		res, err := ts.createSchedule(t.Context(), CreateScheduleArgs{Prompt: "later", When: "in:1m"}, rt)
		require.NoError(t, err)
		require.False(t, res.IsError, res.Output)
		require.NoError(t, ts.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, ts.Stop(t.Context())) })
		synctest.Wait()

		res, err = ts.createSchedule(t.Context(), CreateScheduleArgs{Prompt: "earlier", When: "in:10s"}, rt)
		require.NoError(t, err)
		require.False(t, res.IsError, res.Output)
		synctest.Sleep(10*time.Second - time.Nanosecond)
		require.Empty(t, rt.messages())
		synctest.Sleep(time.Nanosecond)
		require.Len(t, rt.messages(), 1)
		require.Contains(t, rt.messages()[0], "earlier")

		synctest.Sleep(50 * time.Second)
		require.Len(t, rt.messages(), 2)
		require.Contains(t, rt.messages()[1], "later")
		require.Empty(t, ts.store.list())
	})
}

func TestLoopRetriesFailedRecall(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ts := New()
		attempts := 0
		rt := &fakeRuntime{recall: true, recallFn: func() error {
			attempts++
			if attempts == 1 {
				return errors.New("host unavailable")
			}
			return nil
		}}
		res, err := ts.createSchedule(t.Context(), CreateScheduleArgs{Prompt: "retry", When: "in:10s"}, rt)
		require.NoError(t, err)
		require.False(t, res.IsError, res.Output)
		require.NoError(t, ts.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, ts.Stop(t.Context())) })

		synctest.Sleep(10 * time.Second)
		require.Len(t, rt.messages(), 1)
		require.Len(t, ts.store.list(), 1)
		synctest.Sleep(recallRetryDelay - time.Nanosecond)
		require.Len(t, rt.messages(), 1)
		synctest.Sleep(time.Nanosecond)
		require.Len(t, rt.messages(), 2)
		require.Empty(t, ts.store.list())

		synctest.Sleep(recallRetryDelay)
		require.Len(t, rt.messages(), 2)
	})
}

func TestLoopStopPreventsRecall(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ts := New()
		rt := &fakeRuntime{recall: true}
		res, err := ts.createSchedule(t.Context(), CreateScheduleArgs{Prompt: "stopped", When: "in:10s"}, rt)
		require.NoError(t, err)
		require.False(t, res.IsError, res.Output)
		require.NoError(t, ts.Start(t.Context()))
		t.Cleanup(func() { require.NoError(t, ts.Stop(t.Context())) })
		synctest.Wait()

		require.NoError(t, ts.Stop(t.Context()))
		synctest.Sleep(time.Minute)
		require.Empty(t, rt.messages())
	})
}
