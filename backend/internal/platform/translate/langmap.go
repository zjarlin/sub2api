package translate

// isoToTencent 将 ISO 639-1 语言代码映射为腾讯云 TMT 语言代码
var isoToTencent = map[string]string{
	"zh-CN": "zh",
	"zh-TW": "zh-TW",
	"en":    "en",
	"ja":    "ja",
	"ko":    "ko",
	"fr":    "fr",
	"de":    "de",
	"es":    "es",
	"ru":    "ru",
	"it":    "it",
	"pt":    "pt",
	"th":    "th",
	"vi":    "vi",
	"id":    "id",
	"ms":    "ms",
	"ar":    "ar",
	"hi":    "hi",
}

// isoToBaidu 将 ISO 639-1 语言代码映射为百度翻译语言代码
var isoToBaidu = map[string]string{
	"zh-CN": "zh",
	"zh-TW": "cht",
	"en":    "en",
	"ja":    "jp",
	"ko":    "kor",
	"fr":    "fra",
	"de":    "de",
	"es":    "spa",
	"ru":    "ru",
	"it":    "it",
	"pt":    "pt",
	"th":    "th",
	"vi":    "vie",
	"id":    "id",
	"ms":    "may",
	"ar":    "ara",
	"hi":    "hi",
}

// isoToYoudao 将 ISO 639-1 语言代码映射为有道智云语言代码
var isoToYoudao = map[string]string{
	"zh-CN": "zh-CHS",
	"zh-TW": "zh-CHT",
	"en":    "en",
	"ja":    "ja",
	"ko":    "ko",
	"fr":    "fr",
	"de":    "de",
	"es":    "es",
	"ru":    "ru",
	"it":    "it",
	"pt":    "pt",
	"th":    "th",
	"vi":    "vi",
	"id":    "id",
	"ms":    "ms",
	"ar":    "ar",
	"hi":    "hi",
}

// MapLang 将 ISO 639-1 代码转换为指定平台的代码，未找到则返回原始值
func MapLang(isoCode string, mapping map[string]string) string {
	if v, ok := mapping[isoCode]; ok {
		return v
	}
	return isoCode
}
