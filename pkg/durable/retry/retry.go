package retry

import (
	"math"
	"math/rand"
	"time"

	"github.com/dgr237/aws-durable-execution-sdk-go/pkg/durable/config"
)

// ApplyJitter applies jitter to a delay based on the specified strategy.
func ApplyJitter(delay config.Duration, strategy config.JitterStrategy) config.Duration {
	if strategy == config.JitterStrategyNone {
		return delay
	}

	// Initialize random seed if not already done
	seconds := delay.Seconds
	if seconds <= 0 {
		return delay
	}

	switch strategy {
	case config.JitterStrategyFull:
		// Random between 0 and calculated delay
		jittered := rand.Intn(seconds + 1)
		return config.NewDurationFromSeconds(jittered)

	case config.JitterStrategyHalf:
		// Random between 50% and 100% of calculated delay
		minDelay := seconds / 2
		maxDelay := seconds
		jittered := minDelay + rand.Intn(maxDelay-minDelay+1)
		return config.NewDurationFromSeconds(jittered)

	default:
		return delay
	}
}

// CreateRetryStrategy creates a retry strategy with the specified configuration.
type RetryStrategyConfig struct {
	// MaxAttempts is the maximum number of retry attempts (including initial attempt).
	MaxAttempts int

	// InitialDelay is the initial delay before the first retry.
	InitialDelay config.Duration

	// MaxDelay is the maximum delay between retries.
	MaxDelay config.Duration

	// BackoffRate is the exponential backoff multiplier (e.g., 2 for doubling).
	BackoffRate float64

	// Jitter strategy to apply to retry delays.
	Jitter config.JitterStrategy
}

// CreateRetryStrategy creates a retry strategy from configuration.
func CreateRetryStrategy(cfg RetryStrategyConfig) config.RetryStrategy {
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 3
	}
	if cfg.InitialDelay.Seconds <= 0 {
		cfg.InitialDelay = config.NewDurationFromSeconds(1)
	}
	if cfg.MaxDelay.Seconds <= 0 {
		cfg.MaxDelay = config.NewDurationFromSeconds(60)
	}
	if cfg.BackoffRate <= 0 {
		cfg.BackoffRate = 2
	}
	if cfg.Jitter == "" {
		cfg.Jitter = config.JitterStrategyNone
	}

	return func(err error, attempt int) config.RetryDecision {
		if attempt >= cfg.MaxAttempts {
			return config.RetryDecision{ShouldRetry: false}
		}

		// Calculate delay with exponential backoff
		delaySeconds := int(float64(cfg.InitialDelay.Seconds) * math.Pow(cfg.BackoffRate, float64(attempt-1)))

		// Apply max delay cap
		if delaySeconds > cfg.MaxDelay.Seconds {
			delaySeconds = cfg.MaxDelay.Seconds
		}

		delay := config.NewDurationFromSeconds(delaySeconds)

		// Apply jitter
		delay = ApplyJitter(delay, cfg.Jitter)

		return config.RetryDecision{
			ShouldRetry: true,
			Delay:       &delay,
		}
	}
}

// RetryPresets provides common retry strategies.
var RetryPresets = struct {
	// Standard is the default retry strategy with exponential backoff and full jitter.
	Standard func() config.RetryStrategy

	// Immediate retries immediately without delay.
	Immediate func() config.RetryStrategy

	// Fixed retries with a fixed delay.
	Fixed func(delay config.Duration, maxAttempts int) config.RetryStrategy

	// Exponential retries with exponential backoff.
	Exponential func(initialDelay config.Duration, maxAttempts int, backoffRate float64) config.RetryStrategy

	// NoRetry never retries.
	NoRetry func() config.RetryStrategy
}{
	Standard: func() config.RetryStrategy {
		return CreateRetryStrategy(RetryStrategyConfig{
			MaxAttempts:  6,
			InitialDelay: config.NewDurationFromSeconds(5),
			MaxDelay:     config.NewDurationFromSeconds(60),
			BackoffRate:  2,
			Jitter:       config.JitterStrategyFull,
		})
	},

	Immediate: func() config.RetryStrategy {
		return func(err error, attempt int) config.RetryDecision {
			if attempt >= 3 {
				return config.RetryDecision{ShouldRetry: false}
			}
			return config.RetryDecision{ShouldRetry: true}
		}
	},

	Fixed: func(delay config.Duration, maxAttempts int) config.RetryStrategy {
		return func(err error, attempt int) config.RetryDecision {
			if attempt >= maxAttempts {
				return config.RetryDecision{ShouldRetry: false}
			}
			return config.RetryDecision{
				ShouldRetry: true,
				Delay:       &delay,
			}
		}
	},

	Exponential: func(initialDelay config.Duration, maxAttempts int, backoffRate float64) config.RetryStrategy {
		return func(err error, attempt int) config.RetryDecision {
			if attempt >= maxAttempts {
				return config.RetryDecision{ShouldRetry: false}
			}
			delaySeconds := int(float64(initialDelay.Seconds) * math.Pow(backoffRate, float64(attempt)))
			delay := config.NewDurationFromSeconds(delaySeconds)
			return config.RetryDecision{
				ShouldRetry: true,
				Delay:       &delay,
			}
		}
	},

	NoRetry: func() config.RetryStrategy {
		return func(err error, attempt int) config.RetryDecision {
			return config.RetryDecision{ShouldRetry: false}
		}
	},
}

// ExponentialBackoff is a wait strategy with exponential backoff.
func ExponentialBackoff[S any](initialWait config.Duration, maxAttempts int) config.WaitStrategy[S] {
	return func(state S, attempt int) config.WaitStrategyDecision {
		if attempt >= maxAttempts {
			return config.WaitStrategyDecision{ShouldContinue: false}
		}
		delaySeconds := int(float64(initialWait.Seconds) * math.Pow(2, float64(attempt)))
		delay := config.NewDurationFromSeconds(delaySeconds)
		return config.WaitStrategyDecision{
			ShouldContinue: true,
			Delay:          &delay,
		}
	}
}

// FixedBackoff is a wait strategy with fixed delay.
func FixedBackoff[S any](wait config.Duration, maxAttempts int) config.WaitStrategy[S] {
	return func(state S, attempt int) config.WaitStrategyDecision {
		if attempt >= maxAttempts {
			return config.WaitStrategyDecision{ShouldContinue: false}
		}
		return config.WaitStrategyDecision{
			ShouldContinue: true,
			Delay:          &wait,
		}
	}
}

// LinearBackoff is a wait strategy with linear backoff.
func LinearBackoff[S any](initialWait config.Duration, increment config.Duration, maxAttempts int) config.WaitStrategy[S] {
	return func(state S, attempt int) config.WaitStrategyDecision {
		if attempt >= maxAttempts {
			return config.WaitStrategyDecision{ShouldContinue: false}
		}
		delaySeconds := initialWait.Seconds + (increment.Seconds * attempt)
		delay := config.NewDurationFromSeconds(delaySeconds)
		return config.WaitStrategyDecision{
			ShouldContinue: true,
			Delay:          &delay,
		}
	}
}

// FormatDuration formats a duration into a human-readable string.
func FormatDuration(d config.Duration) string {
	dur := time.Duration(d.Seconds) * time.Second

	if dur < time.Minute {
		return dur.String()
	}

	hours := dur / time.Hour
	dur -= hours * time.Hour
	minutes := dur / time.Minute
	dur -= minutes * time.Minute
	seconds := dur / time.Second

	if hours > 0 {
		return formatTime(int(hours), int(minutes), int(seconds))
	}
	if minutes > 0 {
		return formatTimeMinSec(int(minutes), int(seconds))
	}
	return formatTimeSec(int(seconds))
}

func formatTime(h, m, s int) string {
	return formatWith("h", h, "m", m, "s", s)
}

func formatTimeMinSec(m, s int) string {
	return formatWith("m", m, "s", s)
}

func formatTimeSec(s int) string {
	return formatWith("s", s)
}

func formatWith(parts ...interface{}) string {
	result := ""
	for i := 0; i < len(parts); i += 2 {
		if i > 0 {
			result += " "
		}
		result += formatPart(parts[i+1].(int), parts[i].(string))
	}
	return result
}

func formatPart(value int, unit string) string {
	if value == 0 {
		return ""
	}
	return formatValue(value, unit)
}

func formatValue(value int, unit string) string {
	return formatNumber(value) + unit
}

func formatNumber(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	return string(rune('0'+(n/10))) + string(rune('0'+(n%10)))
}
