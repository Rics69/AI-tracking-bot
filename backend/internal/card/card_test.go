package card

import (
	"bytes"
	"image/png"
	"testing"
)

func TestRender(t *testing.T) {
	for name, c := range map[string]Card{
		"goal": {Pose: PoseSaving, Eyebrow: "Цель", Value: "50%", Title: "Ноутбук", Footer: "t.me/SavepointBot?start=ref_abc"},
		"dream": {Pose: PoseProud, Glow: GlowOrange, Eyebrow: "Цена мечты", Value: "10 000 ₽ в месяц", Title: "Очень длинное название цели, которое не помещается в одну строку на карточке",
			Lines: []string{"Около 333 ₽ в день", "К 1 ноября 2027"}},
		"bare": {Value: "7 дней"},
	} {
		t.Run(name, func(t *testing.T) {
			data, err := Render(c)
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			if b := img.Bounds(); b.Dx() != 1080 || b.Dy() != 1920 {
				t.Errorf("size = %v", b)
			}
		})
	}
}
