package handler

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/platform/translate"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/tidwall/gjson"
)

const verticalOperationKey = "vertical_operation"

// 只截获新用户消息；工具续调用不能再次提交已经执行过的媒体任务。
func verticalUserText(body []byte) string {
	format := gjson.GetBytes(body, "text.format.type").String()
	toolChoice := gjson.GetBytes(body, "tool_choice")
	if !gjson.ValidBytes(body) || gjson.GetBytes(body, "previous_response_id").String() != "" ||
		gjson.GetBytes(body, "response_format.type").String() != "" ||
		(format != "" && format != "text") || (toolChoice.Exists() && toolChoice.String() != "auto" && toolChoice.String() != "none") {
		return ""
	}
	for _, field := range []string{"input", "messages"} {
		value := gjson.GetBytes(body, field)
		if !value.Exists() {
			continue
		}
		if value.Type == gjson.String {
			return strings.TrimSpace(value.String())
		}
		items := value.Array()
		if len(items) == 0 {
			return ""
		}
		last := items[len(items)-1]
		if last.Get("role").String() != "user" {
			return ""
		}
		content := last.Get("content")
		if content.IsArray() {
			for _, part := range content.Array() {
				if kind := part.Get("type").String(); kind != "text" && kind != "input_text" {
					return ""
				}
			}
		}
		return strings.Join(messageContentText(content), "\n")
	}
	return ""
}

func verticalDecisionRequest(text string) ([]byte, error) {
	questions := map[string]any{"intent": map[string]any{
		"type": "choice", "instructions": "What does the user want?",
		"criteria": map[string]string{
			"A": "translate text into another language",
			"B": "generate an image or picture",
			"C": "generate a video or movie clip",
			"D": "ask a question, discuss an API, write code, or perform other tasks",
		},
	}}
	return json.Marshal(map[string]any{"model": service.DefaultLayaModel, "state": text, "questions": questions})
}

func verticalDecisionKind(body []byte, threshold float64) string {
	var response struct {
		Model   string `json:"model"`
		Answers map[string]struct {
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
		} `json:"answers"`
	}
	if json.Unmarshal(body, &response) != nil ||
		(response.Model != "laya" && response.Model != "laya-english" && response.Model != "laya-multilingual" && response.Model != "laya-rl-agent") {
		return ""
	}
	answer := response.Answers["intent"]
	total := 0.0
	for _, option := range []string{"A", "B", "C", "D"} {
		probability, exists := answer.Probabilities[option]
		if !exists || probability < 0 || probability > 1 {
			return ""
		}
		total += probability
	}
	if total < 0.99 || total > 1.01 || answer.Probabilities[answer.Choice] < threshold {
		return ""
	}
	return map[string]string{"A": "translation", "B": "image_generation", "C": "video_generation"}[answer.Choice]
}

var verticalCommandPrefix = regexp.MustCompile(`(?i)^(?:(?:请帮我|帮我|麻烦|请|能否|可以|能不能)\s*|please\s+)+`)
var englishMediaCommand = regexp.MustCompile(`(?i)^(?:generate|create|draw|render|make)\s+(?:an?\s+|one\s+)?(?:image|picture|illustration|photo|video|clip)\b`)

// Laya 能识别领域，但实测会把接口讨论也归到该领域；命令形态单独保证执行边界。
func verticalExplicitKind(text string) string {
	header := text
	if separator := strings.IndexAny(header, ":：\n"); separator >= 0 {
		header = header[:separator]
	}
	header = strings.ToLower(strings.TrimSpace(verticalCommandPrefix.ReplaceAllString(header, "")))
	if hasAny(header, []string{"然后", "接着", "再解释", "并解释", "并修改", "并运行", "and explain", "then ", "write code", "implement", "how ", "如何", "怎么", "不要", "不用", "别", "接口", "功能", "代码", "文件", "项目"}) {
		return ""
	}
	if strings.HasSuffix(header, "?") || strings.HasSuffix(header, "？") || strings.HasSuffix(header, "吗") ||
		hasAny(header, []string{"多少钱", "收费", "需要什么", "支持哪些", "区别", "能做什么"}) {
		return ""
	}
	imageMention := hasAny(header, []string{"图片", "图像", "插画", "照片", "image", "picture", "illustration", "photo"})
	videoMention := hasAny(header, []string{"视频", "短片", "video", "clip"})
	if imageMention && videoMention && !strings.HasPrefix(header, "翻译") && !strings.HasPrefix(header, "translate ") {
		return ""
	}
	if strings.HasPrefix(header, "翻译") || strings.HasPrefix(header, "translate ") || strings.HasPrefix(header, "把") || strings.HasPrefix(header, "将") {
		if verticalTranslationRequest(text) != nil {
			return "translation"
		}
		return ""
	}
	if hasAny(strings.ToLower(text), []string{"然后", "接着", "再解释", "并解释", "并修改", "并运行", "and explain", "then modify", "then run", "write code", "implement"}) {
		return ""
	}
	if englishMediaCommand.MatchString(header) {
		if hasAny(header, []string{"video", "clip"}) {
			return "video_generation"
		}
		return "image_generation"
	}
	if strings.HasPrefix(header, "生成") || strings.HasPrefix(header, "画") || strings.HasPrefix(header, "绘制") {
		if hasAny(header, []string{"视频", "短片"}) {
			return "video_generation"
		}
		if hasAny(header, []string{"图片", "图像", "插画", "照片", "一张", "一幅"}) {
			return "image_generation"
		}
	}
	return ""
}

const translationLanguagePattern = `英语|英文|中文|汉语|日语|日文|韩语|韩文|法语|法文|德语|德文|西班牙语|俄语|俄文|意大利语|葡萄牙语|阿拉伯语|English|Chinese|Japanese|Korean|French|German|Spanish|Russian|Italian|Portuguese|Arabic`

var translationTargetPattern = regexp.MustCompile(`(?i)(?:翻译(?:成|为|到)|译成|译为|翻成|(?:into|to)\s+)(` + translationLanguagePattern + `)`)
var englishTranslationPattern = regexp.MustCompile(`(?is)^(?:please\s+)?translate\s+(.+?)\s+(?:into|to)\s+(` + translationLanguagePattern + `)[.!?。]*$`)
var translationCommandPrefix = regexp.MustCompile(`(?i)^(?:(?:请|麻烦|帮我|请帮我|把|将)\s*)+`)

// Laya 只分类，不生成参数；源文本必须能从明确句型中无损取得。
func verticalTranslationRequest(text string) *translate.TranslateRequest {
	match := translationTargetPattern.FindStringSubmatchIndex(text)
	if match == nil {
		return nil
	}
	language := strings.ToLower(text[match[2]:match[3]])
	languages := map[string]string{
		"英语": "en", "英文": "en", "english": "en", "中文": "zh", "汉语": "zh", "chinese": "zh",
		"日语": "ja", "日文": "ja", "japanese": "ja", "韩语": "ko", "韩文": "ko", "korean": "ko",
		"法语": "fr", "法文": "fr", "french": "fr", "德语": "de", "德文": "de", "german": "de",
		"西班牙语": "es", "spanish": "es", "俄语": "ru", "俄文": "ru", "russian": "ru",
		"意大利语": "it", "italian": "it", "葡萄牙语": "pt", "portuguese": "pt", "阿拉伯语": "ar", "arabic": "ar",
	}
	source := ""
	after := text[match[1]:]
	if separator := strings.IndexAny(after, ":：\n"); separator >= 0 && strings.TrimSpace(after[:separator]) == "" {
		_, size := utf8.DecodeRuneInString(after[separator:])
		source = strings.TrimSpace(after[separator+size:])
	} else if english := englishTranslationPattern.FindStringSubmatch(text); english != nil {
		source = strings.TrimSpace(english[1])
	} else if strings.Trim(after, " \t\r\n。.!！") == "" {
		source = translationCommandPrefix.ReplaceAllString(strings.TrimSpace(text[:match[0]]), "")
	}
	source = strings.TrimSpace(source)
	if source == "" || source == "这个" || source == "这段话" || source == "以下内容" || source == "下面的内容" || source == "it" || source == "this" {
		return nil
	}
	return &translate.TranslateRequest{Text: []string{source}, TargetLang: languages[language], Format: "text"}
}
