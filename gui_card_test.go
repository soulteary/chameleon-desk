package chameleon

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestGUICardRoundTrip(t *testing.T) {
	rows := make([][]int, 64)
	for i := range rows {
		rows[i] = make([]int, 16)
		rows[i][0] = i
	}
	b, _ := json.Marshal(map[string]any{"id": "test", "name": "测试卡", "uid": "01020304", "tag": 1001, "sak": 8, "atqa": []int{4, 0}, "data": rows, "extra": map[string]any{"custom": "preserve"}, "folderId": "folder", "newField": 42})
	c, e := ParseGUICard(b)
	if e != nil {
		t.Fatal(e)
	}
	image, e := c.EmulatorImage()
	if e != nil || len(image.Data) != 1024 {
		t.Fatal(e)
	}
	image.Data[0] = 255
	image.SHA256 = imageDigest(image.Data)
	c, e = c.WithImage(image)
	if e != nil {
		t.Fatal(e)
	}
	out, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Contains(out, []byte(`"newField":42`)) || !bytes.Contains(out, []byte(`"custom":"preserve"`)) || !bytes.Contains(out, []byte(`"atqa":[4,0]`)) {
		t.Fatal(string(out))
	}
	again, e := ParseGUICard(out)
	if e != nil || again.Data[0][0] != 255 {
		t.Fatal(e)
	}
}
func TestGUICardRejectsInvalid(t *testing.T) {
	for _, b := range []string{`null`, `{}`, `{"id":"a","name":"a","uid":"01","tag":1001,"sak":8,"atqa":[4,0],"data":[]}`, `{"id":"a","name":"a","uid":"01020304","tag":1001,"sak":8,"atqa":[256,0],"data":[]}`} {
		if _, e := ParseGUICard([]byte(b)); e == nil {
			t.Fatal(b)
		}
	}
}
func TestGUIDictionary(t *testing.T) {
	var d GUIDictionary
	if e := json.Unmarshal([]byte(`{"id":"d","name":"keys","keys":[[255,255,255,255,255,255]],"color":"123456","folderId":"f"}`), &d); e != nil {
		t.Fatal(e)
	}
	d, e := d.WithText("# keys\nFFFFFFFFFFFF\nffffffffffff\n010203040506\n")
	if e != nil || len(d.Keys) != 2 {
		t.Fatal(d, e)
	}
	out, e := json.Marshal(d)
	if e != nil || !bytes.Contains(out, []byte(`"folderId":"f"`)) {
		t.Fatal(string(out), e)
	}
	if text, e := d.Text(); e != nil || text != "FFFFFFFFFFFF\n010203040506\n" {
		t.Fatal(text, e)
	}
	if _, e = d.WithText("ABCD"); e == nil {
		t.Fatal("short key accepted")
	}
}
