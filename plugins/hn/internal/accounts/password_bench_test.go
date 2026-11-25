package accounts

import (
	"strconv"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func BenchmarkHashCost(b *testing.B) {
	for _, c := range []int{10, 11, 12} {
		b.Run(strconv.Itoa(c), func(b *testing.B) {
			for b.Loop() {
				_, _ = bcrypt.GenerateFromPassword([]byte("correct horse battery"), c)
			}
		})
	}
}

func BenchmarkCheckPassword(b *testing.B) {
	h, err := hashPassword("correct horse battery")
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		checkPassword(h, "wrong horse battery")
	}
}
