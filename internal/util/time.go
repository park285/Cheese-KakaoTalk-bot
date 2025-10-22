package util

import (
    "math"
    "time"
)

var kstLocation *time.Location

func init() {
    var err error
    kstLocation, err = time.LoadLocation("Asia/Seoul")
    if err != nil {
        kstLocation = time.FixedZone("KST", 9*60*60)
    }
}

func ToKST(t time.Time) time.Time {
    return t.In(kstLocation)
}

func FormatKST(t time.Time, layout string) string {
    return t.In(kstLocation).Format(layout)
}

func NowKST() time.Time {
    return time.Now().In(kstLocation)
}

func MinutesUntilCeil(target *time.Time, reference time.Time) int {
    if target == nil {
        return -1
    }

    if target.Before(reference) {
        return -1
    }

    duration := target.Sub(reference)
    minutesUntil := math.Ceil(duration.Minutes())
    if minutesUntil < 0 {
        return -1
    }

    return int(minutesUntil)
}

