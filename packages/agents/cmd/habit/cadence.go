package main

// The tapering rule: how often to check in on a habit, from its entries.
// cadenceRule (below) is the same rule in words, for `habit cadence --help`
// and agents/habit-helper/README.md.

// ladder is the intervals, in days, the rule moves between.
var ladder = []int{1, 2, 4, 7, 14, 30, 60}

const (
	// minRun is the fewest done days in a row that grow the interval.
	minRun = 3
)

const cadenceRule = `The rule (applied by habit log, replayed over all of a habit's entries in day
order, so logging a day again or filling in an old day gives the same result):

  Intervals: 1, 2, 4, 7, 14, 30, 60 days. A habit starts at 1.
  done:   the run of done days grows by one. When the run reaches the
          current interval (at least 3), the interval moves up a step
          and the run starts again.
  missed: the first miss in a row moves the interval down a step (a single
          slip is fine); a second miss in a row resets it to 1 day.
          Either way the run starts again.
  Days with no entry are skipped: they neither break nor extend a run.

  next check = the latest logged day + the interval, and never before
  tomorrow (logging is the check-in).

So a habit done every day goes 1 -> 2 after 3 days, -> 4 after 3 more,
-> 7 after 4, -> 14 after 7, -> 30 after 14, -> 60 after 30.

An override (habit cadence <habit> --every N --reason "...") pins the interval:
later logs still move next check (latest logged day + N) and the streak, but
not the interval, until habit cadence <habit> --auto hands it back to the rule.`

// ruleState is the outcome of replaying a habit's entries.
type ruleState struct {
	Interval int
	// Streak and Misses are the done and missed entries in a row ending at
	// Last (one of them is 0).
	Streak, Misses int
	// Last is the latest logged day; zero if there are no entries.
	Last Day
}

type dayOutcome struct {
	Day     Day
	Outcome string
}

func stepUp(i int) int {
	for _, v := range ladder {
		if v > i {
			return v
		}
	}
	return ladder[len(ladder)-1]
}

func stepDown(i int) int {
	prev := ladder[0]
	for _, v := range ladder {
		if v >= i {
			return prev
		}
		prev = v
	}
	return prev
}

// replay applies the rule to entries, which must be in day order.
func replay(entries []dayOutcome) ruleState {
	s := ruleState{Interval: ladder[0]}
	run := 0
	for _, e := range entries {
		s.Last = e.Day
		switch e.Outcome {
		case "done":
			s.Streak++
			s.Misses = 0
			run++
			if run >= max(minRun, s.Interval) {
				s.Interval = stepUp(s.Interval)
				run = 0
			}
		case "missed":
			s.Misses++
			s.Streak = 0
			run = 0
			if s.Misses == 1 {
				s.Interval = stepDown(s.Interval)
			} else {
				s.Interval = ladder[0]
			}
		}
	}
	return s
}

// nextCheck is when to check in next after the latest logged day, with the
// given interval: never before tomorrow. With no entries it is today.
func nextCheck(last Day, interval int, today Day) Day {
	if last.IsZero() {
		return today
	}
	return maxDay(last.AddDays(interval), today.AddDays(1))
}
