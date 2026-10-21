package matching

import (
 "testing"
 "github.com/pokemon/poracleng/processor/internal/db"
)

func TestDittoThirdDraw(t *testing.T) {
 for _, c := range []struct{enc string; want int}{
  {"1",3},{"83",0},{"2633",38},{"4340",0},
 } {
  got,ok:=DittoShinyRoll(c.enc,"0123456789abcdef")
  if !ok || got!=c.want {t.Fatalf("enc=%s got=%d ok=%v want=%d",c.enc,got,ok,c.want)}
 }
 for _,enc:=range []string{"bad","-1","18446744073709551616"} {
  if _,ok:=DittoShinyRoll(enc,"0123456789abcdef");ok {t.Fatal("invalid encounter accepted")}
 }
 if _,ok:=DittoShinyRoll("1","");ok {t.Fatal("empty player accepted")}
}

func TestDittoPersonalFilter(t *testing.T) {
 defer shinyReleased.Store(nil)
 defer shinyBoosted.Store(nil)
 SetShinyReleased([]int{132,25})
 boosted:=map[int]shinyBoost{132:{Rate:25}}
 shinyBoosted.Store(&boosted)
 humans:=map[string]*db.Human{"linked":{ID:"linked",ShinyPlayerID:"0123456789abcdef"}}
 for _,c:=range []struct{enc string; species,want int}{
  {"83",132,2}, {"2633",132,1}, {"4340",132,2},
  {"83",25,1}, {"2633",25,2},
 } {
  rules:=[]*db.MonsterTracking{{ID:"linked",ShinyPredict:1},{ID:"unlinked",ShinyPredict:1},{ID:"plain",ShinyPredict:0}}
  got:=filterPersonalShiny(c.enc,c.species,0,0,rules,humans)
  if len(got)!=c.want {t.Fatalf("enc=%s species=%d got=%d want=%d",c.enc,c.species,len(got),c.want)}
  if got[len(got)-1].ID!="plain" {t.Fatal("normal rule lost")}
 }
 SetShinyReleased([]int{25})
 if got:=filterPersonalShiny("83",132,0,0,[]*db.MonsterTracking{{ID:"linked",ShinyPredict:1}},humans);len(got)!=0 {t.Fatal("release gate bypassed")}
}
