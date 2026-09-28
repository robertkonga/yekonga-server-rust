package helper

import (
	"fmt"
	"strconv"
	"strings"
)

func NextSerial(current string, totalLength int) string {
	if len(current) != totalLength {
		return ""
	}

	// split letters and digits
	i := 0
	for i < len(current) && current[i] >= 'A' && current[i] <= 'Z' {
		i++
	}

	prefix := current[:i]
	numberPart := current[i:]

	number, err := strconv.Atoi(numberPart)
	if err != nil {
		return ""
	}

	maxNumber := pow10(len(numberPart)) - 1

	// increment number
	number++

	// if limit reached, increment letters
	if number > maxNumber {
		prefix = nextPrefix(prefix)

		// keep total length = 8
		numberDigits := totalLength - len(prefix)

		if numberDigits <= 0 {
			return ""
		}

		number = 1

		return fmt.Sprintf(
			"%s%0*d",
			prefix,
			numberDigits,
			number,
		)
	}

	return fmt.Sprintf(
		"%s%0*d",
		prefix,
		len(numberPart),
		number,
	)
}

func nextPrefix(s string) string {
	r := []rune(s)

	for i := len(r) - 1; i >= 0; i-- {
		if r[i] < 'Z' {
			r[i]++
			return string(r)
		}

		r[i] = 'A'
	}

	// grow prefix if all Z
	return "A" + strings.Repeat("A", len(r))
}

func pow10(n int) int {
	result := 1

	for i := 0; i < n; i++ {
		result *= 10
	}

	return result
}
