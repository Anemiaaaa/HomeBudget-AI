package expense

import "testing"

func TestParseMoney(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "integer", input: "350", want: "350.00"},
		{name: "comma", input: "350,5", want: "350.50"},
		{name: "spaced", input: "1 200,50", want: "1200.50"},
		{name: "minimum", input: "0.01", want: "0.01"},
		{name: "zero", input: "0", wantErr: true},
		{name: "negative", input: "-1", wantErr: true},
		{name: "too precise", input: "1.001", wantErr: true},
		{name: "exponent", input: "1e3", wantErr: true},
		{name: "empty fraction", input: "1.", wantErr: true},
		{name: "too large", input: "10000000000.00", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := ParseMoney(test.input)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseMoney(%q) error = nil", test.input)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMoney(%q) error = %v", test.input, err)
			}
			if got.String() != test.want {
				t.Fatalf("ParseMoney(%q) = %q, want %q", test.input, got.String(), test.want)
			}
		})
	}
}
