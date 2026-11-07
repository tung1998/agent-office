package textrank

import "testing"

func TestWordsExpandsAbbreviations(t *testing.T) {
	got := Words("ko có đoạn giải thích dài")
	want := Words("không có đoạn giải thích dài")
	if len(got) != len(want) {
		t.Fatalf("Words(ko) = %v, want same as %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("Words(ko) = %v, want %v", got, want)
		}
	}
}

func TestWordsAbbreviationWholeWordOnly(t *testing.T) {
	// "vd1" must not be treated as the abbreviation "vd".
	got := Words("vd1 hello")
	if slicesContains(got, "vi") || slicesContains(got, "du") {
		t.Fatalf("Words(vd1) expanded a substring match: %v", got)
	}
}

func TestRankFindsNoteWithAbbreviatedQuery(t *testing.T) {
	// Note written in full, query abbreviated.
	notes := []string{
		Normalize("không có đoạn giải thích dài"),
		Normalize("một ghi chú khác không liên quan"),
	}
	hits, _ := Rank(notes, Words("ko có đoạn giải thích dài"))
	if len(hits) == 0 || hits[0].At != 0 {
		t.Fatalf("Rank did not find the note for abbreviated query: %+v", hits)
	}
}

func TestRankFindsAbbreviatedNoteWithFullQuery(t *testing.T) {
	// Note written abbreviated, query in full: both directions must work,
	// since notes in this project are often written with abbreviations.
	notes := []string{
		Normalize("ko có đoạn giải thích dài"),
		Normalize("một ghi chú khác không liên quan"),
	}
	hits, _ := Rank(notes, Words("không có đoạn giải thích dài"))
	if len(hits) == 0 || hits[0].At != 0 {
		t.Fatalf("Rank did not find the abbreviated note for a full-word query: %+v", hits)
	}
}

func TestExpandIgnoresStuckPunctuation(t *testing.T) {
	cases := map[string]string{
		"vd:":  "ví dụ:",
		"(ko":  "(không",
		"ko,":  "không,",
		"vd1":  "vd1", // not the abbreviation "vd": a digit is part of the core word
		"vd.":  "ví dụ.",
		"":     "",
		"----": "----",
	}
	for in, want := range cases {
		if got := Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRankFindsNoteWithStuckPunctuation(t *testing.T) {
	notes := []string{Normalize("vd: thêm field mới vào form")}
	if hits, _ := Rank(notes, Words("vd")); len(hits) == 0 {
		t.Fatalf("note 'vd:' not found by query 'vd'")
	}
	if hits, _ := Rank(notes, Words("ví dụ")); len(hits) == 0 {
		t.Fatalf("note 'vd:' not found by query 'ví dụ'")
	}
	notes2 := []string{Normalize("ko, chưa làm xong")}
	if hits, _ := Rank(notes2, Words("ko")); len(hits) == 0 {
		t.Fatalf("note 'ko,' not found by query 'ko'")
	}
}

func TestRankFindsAbbreviatedWordVs(t *testing.T) {
	// "CAS vs service" must still be found when searching "vs".
	notes := []string{Normalize("CAS vs service")}
	hits, _ := Rank(notes, Words("vs"))
	if len(hits) == 0 {
		t.Fatalf("Rank did not find the note for 'vs': %+v", hits)
	}
}

func slicesContains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
