package version

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		input   string
		want    Semver
		wantErr bool
	}{
		{"0.1.0", Semver{0, 1, 0}, false},
		{"1.2.3", Semver{1, 2, 3}, false},
		{"10.20.30", Semver{10, 20, 30}, false},
		{"0.0.0", Semver{0, 0, 0}, false},
		{"", Semver{}, true},
		{"1.2", Semver{}, true},
		{"1.2.3.4", Semver{}, true},
		{"v1.2.3", Semver{}, true},
		{"a.b.c", Semver{}, true},
		{"1.2.-1", Semver{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Parse(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("Parse(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.1.0", "1.0.9", 1},
		{"2.0.0", "1.9.9", 1},
		{"0.1.0", "0.1.0", 0},
		{"0.2.0", "0.1.0", 1},
	}
	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			got, err := Compare(tt.a, tt.b)
			if err != nil {
				t.Fatalf("Compare(%q, %q) error: %v", tt.a, tt.b, err)
			}
			if got != tt.want {
				t.Fatalf("Compare(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestAtLeast(t *testing.T) {
	tests := []struct {
		version    string
		constraint string
		want       bool
		wantErr    bool
	}{
		// Empty constraint = any version.
		{"0.1.0", "", true, false},
		{"99.0.0", "", true, false},
		// Bare version = minimum.
		{"0.1.0", "0.1.0", true, false},
		{"0.2.0", "0.1.0", true, false},
		{"0.0.9", "0.1.0", false, false},
		// >= prefix.
		{"0.1.0", ">=0.1.0", true, false},
		{"1.0.0", ">=0.1.0", true, false},
		{"0.0.1", ">=0.1.0", false, false},
		{"2.0.0", ">=1.5.0", true, false},
		// Errors.
		{"bad", "0.1.0", false, true},
		{"0.1.0", "bad", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.version+"_"+tt.constraint, func(t *testing.T) {
			got, err := AtLeast(tt.version, tt.constraint)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AtLeast(%q, %q) error = %v, wantErr %v", tt.version, tt.constraint, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("AtLeast(%q, %q) = %v, want %v", tt.version, tt.constraint, got, tt.want)
			}
		})
	}
}
