package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// maxHistoryMonths 限制单次历史查询跨越的月份数，避免一次请求放大成上百次上游调用。
const maxHistoryMonths = 12

// Tianqi2345Provider 使用 2345 天气（tianqi.2345.com）的免密钥公开历史页，覆盖国内历史日线数据。
//
// 该接口返回 GBK 编码的 JSON，其中 data 字段是 HTML 表格；location 需为 2345 地区编码
// （例如洛阳 57073），城市名返回 ErrLocationUnsupported。
type Tianqi2345Provider struct {
	endpoint string
	client   *http.Client
}

// NewTianqi2345Provider 创建 2345 天气适配器。
func NewTianqi2345Provider() *Tianqi2345Provider {
	return &Tianqi2345Provider{endpoint: "https://tianqi.2345.com/Pc/GetHistory", client: newClient()}
}

func (p *Tianqi2345Provider) Name() string { return "tianqi2345" }

func (p *Tianqi2345Provider) Capabilities() Capabilities {
	return Capabilities{History: true}
}

func (p *Tianqi2345Provider) Fetch(ctx context.Context, req *WeatherRequest) (*WeatherResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("tianqi2345: request is required")
	}
	areaID := trimmed(req.Location)
	if !isNumericCode(areaID) {
		return nil, ErrLocationUnsupported
	}
	start, end, err := historyRange(req)
	if err != nil {
		return nil, fmt.Errorf("tianqi2345: %w", err)
	}
	months, err := historyMonths(start, end)
	if err != nil {
		return nil, err
	}
	response := &WeatherResponse{Provider: p.Name(), Resolved: areaID, Attribution: "2345 天气 tianqi.2345.com"}
	for _, month := range months {
		days, err := p.fetchMonth(ctx, areaID, month.year, month.month)
		if err != nil {
			return nil, err
		}
		for _, day := range days {
			if day.Date < start || day.Date > end {
				continue
			}
			response.History = append(response.History, day)
		}
	}
	if len(response.History) == 0 {
		return nil, fmt.Errorf("tianqi2345: no history rows in the requested range")
	}
	return response, nil
}

type calendarMonth struct {
	year  int
	month int
}

// historyMonths 展开 [start, end] 覆盖的月份，最多 maxHistoryMonths 个月。
func historyMonths(start, end string) ([]calendarMonth, error) {
	startDate, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, fmt.Errorf("start must be YYYY-MM-DD")
	}
	endDate, err := time.Parse("2006-01-02", end)
	if err != nil {
		return nil, fmt.Errorf("end must be YYYY-MM-DD")
	}
	if endDate.Before(startDate) {
		return nil, fmt.Errorf("end must not be earlier than start")
	}
	months := make([]calendarMonth, 0, 12)
	cursor := time.Date(startDate.Year(), startDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	last := time.Date(endDate.Year(), endDate.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !cursor.After(last) {
		months = append(months, calendarMonth{year: cursor.Year(), month: int(cursor.Month())})
		if len(months) > maxHistoryMonths {
			return nil, fmt.Errorf("history range must not exceed %d months", maxHistoryMonths)
		}
		cursor = cursor.AddDate(0, 1, 0)
	}
	return months, nil
}

func (p *Tianqi2345Provider) fetchMonth(ctx context.Context, areaID string, year, month int) ([]DailyWeather, error) {
	query := "areaInfo[areaId]=" + areaID + "&areaInfo[areaType]=2&date[year]=" + strconv.Itoa(year) + "&date[month]=" + strconv.Itoa(month)
	headers := map[string]string{
		"referer":          "https://tianqi.2345.com/wea_history/" + areaID + ".htm",
		"user-agent":       browserUserAgent,
		"x-requested-with": "XMLHttpRequest",
	}
	body, err := get(ctx, p.client, p.endpoint+"?"+query, headers)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeGBK(body)
	if err != nil {
		return nil, fmt.Errorf("tianqi2345: decode response failed")
	}
	if strings.Contains(decoded, "抱歉，暂无") {
		return nil, nil
	}
	var payload struct {
		Code int    `json:"code"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal([]byte(decoded), &payload); err != nil {
		return nil, fmt.Errorf("tianqi2345: invalid JSON response")
	}
	if payload.Code != 1 {
		return nil, fmt.Errorf("tianqi2345: upstream code %d", payload.Code)
	}
	return parseHistoryTable(payload.Data), nil
}

// decodeGBK 优先按 UTF-8 解析，失败时回退 GBK。
func decodeGBK(body []byte) (string, error) {
	if utf8, err := simplifiedchinese.GBK.NewDecoder().Bytes(body); err == nil {
		return string(utf8), nil
	}
	return "", fmt.Errorf("weather: response is not UTF-8 or GBK")
}

// parseHistoryTable 解析历史天气 HTML 表格，单元格顺序为日期、最高温、最低温、天气、风向、空气质量。
func parseHistoryTable(table string) []DailyWeather {
	scope := table
	if _, rest, found := strings.Cut(table, "<tbody"); found {
		if body, _, closed := strings.Cut(rest, "</tbody>"); closed {
			scope = body
		}
	}
	days := make([]DailyWeather, 0, 31)
	for _, row := range strings.Split(scope, "<tr") {
		cells := make([]string, 0, 8)
		for index, cell := range strings.Split(row, "<td") {
			if index == 0 {
				continue
			}
			value := cell
			if _, rest, found := strings.Cut(cell, ">"); found {
				value = rest
			}
			if head, _, closed := strings.Cut(value, "</td>"); closed {
				value = head
			}
			value = strings.NewReplacer("\n", "", "\r", "", "&nbsp;", " ").Replace(value)
			cells = append(cells, strings.TrimSpace(html.UnescapeString(value)))
		}
		if len(cells) < 4 {
			continue
		}
		fields := strings.Fields(cells[0])
		if len(fields) == 0 || !isISODate(fields[0]) {
			continue
		}
		day := DailyWeather{
			Date:  fields[0],
			Week:  weekday(fields[0]),
			HighC: number(cells[1]),
			LowC:  number(cells[2]),
		}
		if len(fields) > 1 {
			day.Week = fields[1]
		}
		if len(cells) > 3 {
			day.Condition = cells[3]
		}
		if len(cells) > 4 {
			day.WindDir = cells[4]
		}
		if len(cells) > 5 {
			if tokens := strings.Fields(cells[5]); len(tokens) > 0 {
				if value, err := strconv.Atoi(tokens[0]); err == nil {
					day.AirQuality = value
				}
			}
		}
		days = append(days, day)
	}
	return days
}

func isISODate(value string) bool {
	if len(value) != 10 {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

// number 从 "10℃" 之类的文本里提取数值。
func number(value string) float64 {
	filtered := strings.Map(func(character rune) rune {
		if (character >= '0' && character <= '9') || character == '-' || character == '.' {
			return character
		}
		return -1
	}, value)
	return parseFloat(filtered)
}
