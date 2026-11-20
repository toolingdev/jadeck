package stage

import (
	"fmt"
	"strconv"
)

func sprintf(f string, a ...any) string { return fmt.Sprintf(f, a...) }
func itoa(n int) string                 { return strconv.Itoa(n) }
