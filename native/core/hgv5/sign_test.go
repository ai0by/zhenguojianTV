package hgv5

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestSignPOSTCoversBody(t *testing.T) {
	ent := &Entropy{NowSecs: 1_700_000_000, GorgonRandom: 0x1234, HeliosRandom: 1, MessageRandom: 1, Random: 1, PackedRandom: 1, EnvLaunch: 100, EnvPID: 10001}
	body := []byte(`{"series_id":"1"}`)
	pairs := [][2]string{{"aid", "8662"}}
	query, headers := SignPOST("/novel/player/video_detail/v1/", pairs, 1_700_000_000_123, "1", "7.3.9.32", body, ent)
	sum := md5Raw(body)
	if headers["x-ss-stub"] != strings.ToUpper(hex.EncodeToString(sum[:])) || headers["Content-Type"] == "" {
		t.Fatalf("stub headers missing")
	}
	if !strings.Contains(query, "aid=8662") || !strings.Contains(query, "ts=") {
		t.Fatalf("query = %s", query)
	}
	_, getHeaders := SignGET(pairs, 1_700_000_000_123, "1", "7.3.9.32", ent)
	if getHeaders["x-gorgon"] == headers["x-gorgon"] || getHeaders["x-ss-stub"] != "" {
		t.Fatal("POST signature did not cover the body")
	}
}

func TestSM3ABC(t *testing.T) {
	sum := sm3Sum([]byte("abc"))
	const want = "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"
	if hex.EncodeToString(sum[:]) != want {
		t.Fatalf("sm3 = %x", sum)
	}
}

// The device and install IDs are placeholders; the expected values are this
// implementation's output for them, checked against the reference algorithm
// with a real device before the IDs were replaced.
func TestGorgonHeliosAndMedusaMatchReference(t *testing.T) {
	const query = "iid=7100000000000012&device_id=7100000000000011&ac=wifi&channel=update_64" +
		"&aid=8662&app_name=novelread&version_code=71332&version_name=7.1.3.32" +
		"&device_platform=android&os=android&ssmix=a&device_type=25053RT47C" +
		"&device_brand=Redmi&language=zh&os_api=36&os_version=16" +
		"&manifest_version_code=71332&resolution=1280*2772&dpi=520" +
		"&update_version_code=71332&host_abi=arm64-v8a&dragon_device_type=phone" +
		"&pv_player=71332&compliance_status=0&need_personal_recommend=1" +
		"&player_so_load=1&is_android_pad_screen=0" +
		"&ts=1700000000&_rticket=1700000000123"
	got := xGorgon(query, []byte(`{"a":1}`), true, 1_700_000_000, 0x1234)
	if got != "84043412400154ed198c3230d5ff1364e71df6105a46126993c0" {
		t.Fatalf("gorgon = %s", got)
	}
	if assembleHelios(1_700_000_000, 2_028_160_147) != "k0TjeBWFFnRXfkeKlh3Hmx1jLjrfhgCasqsF4XvB1+0eB1Mw" {
		t.Fatalf("helios = %s", assembleHelios(1_700_000_000, 2_028_160_147))
	}
	hash, seed := keyHash(medusaSignKey(), 2_378_451_149)
	if hex.EncodeToString(hash[:]) != "1b35023431b99a949ab7b70fc50a30216076492a6229d102a7a33fa3a1104170" {
		t.Fatalf("key hash = %x", hash)
	}
	if hex.EncodeToString(seed[:]) != "b0dff9ff" {
		t.Fatalf("seed = %x", seed)
	}
	mixed := xmxor([]byte("0123456789abcdefghij"), hash[:])
	if hex.EncodeToString(mixed) != "5c69c37694106e8cbdb2e074619a1dda6c5a5a65" {
		t.Fatalf("xmxor = %x", mixed)
	}
	medusa := assembleMedusa(
		"https://api5-normal-sinfonlineb.fqnovel.com/x/v1?aid=8662&device_id=7100000000000011&ts=1700000000",
		[]byte(`{"a":1}`), true, 1_700_000_000, "7100000000000011", "7.1.3.32",
		Entropy{EnvLaunch: 103, EnvPID: 11691, NowSecs: 1_700_000_000, MessageRandom: 1_273_073_252, Random: 2_378_451_149, PackedRandom: 2_253_366_658},
	)
	const want = "A/FTZfcZDJ/XJo9e1tubFVc+Mn3NSAABthXCoU+OAdYPONbwm9+RyUZ1cbOs05Yl1aeIPCOGtW+GK2b4gMoFqK69oa1CtJNpHvFQ90KOznW75N3Iac3SIC2SIL/AEUa1LqRKHJc0OVAOzCUSF1PpwjNvKQMxteguS4MtQqRWQiRv25xZVCxPuR4K155Yu2XGsfoblNT6sUx3M9mDqh8WhMpoRbPjSwAij42sVoYMhYUMcyYL73j0agwgsSu/bMNgpLXppp9Hd7KwpiRPFCyDMGP892DPSagFV+ndwVa0M9GGxB3ksAgAUUr91XV5uEUA6/MJpv7AUUEpFfk101bKFHLmflVjL4B9K4OEos6OgkIx2gloIAOULcEBroYDODhlyRqqKT7TX53G09xaFF/YpqtuOC6ZkfG6MOBtYCrCeqgLtoubTaE/xh0VOVRVS/mQWvXYhS00Tqcb/ywQaQzDm1eBAxc1SgDLnzwzLAPLrmDI4rDLqaAWcL6yhsqXydzBShsdZOwreQE9pHzhm3Kaw1ZdV/FLHMqIKnuGoGOOAv19acvxPext43+ckznl5fV5pIj1txHIxcgg8/fQMx0xYM9ubLBnL3lZr+x7226u1kUngjM/IFDSCsVcs3QRzyjAGo+2SWLa8Sicyiq1QsQB5Xw0KWMNlA7lA8dRmxrs2ANytt9GeELT6TZqAWwwoCRbae6aXm+j2qgybmCrmffHBE3gZWgQ8pTjpWWRedesWrwwfioLmyWqRmZRl9TgaWiJpFQZwLFz9WnDhbk8/rFeQjtPH+/my02pcdEXl2Onwwg7/iS5nhWwmKn/k378VIdufE0rFeIAKs9npH8SwAuxaT0GEiWv0AZuXppB6clslZlunKcJHHZK2g/UtbSWEodVLcYPhnFt7cZe46GeFxdSUxNb26BpkjTtwLQGqVFFlFqGb5ijjxz6BVEBpXbZVpLW0+OKFDlfKj166UTiQdk4GfWp28mwvGYFiVqlBOew//nfsP/4n8SN"
	if medusa != want {
		t.Fatalf("medusa mismatch\n got %s\nwant %s", medusa, want)
	}
	if strings.Contains(medusa, "device_token") {
		t.Fatal("medusa leaked a token field name")
	}
}
