package write

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

const auditFieldMax = 200

func summarize(tool string, args map[string]any) string {
	if args == nil {
		args = map[string]any{}
	}
	switch tool {
	case ToolMailSend:
		return fmt.Sprintf("mail to %d recipients, subject '%s'",
			lenAny(args["recipients"], args["to"]), argString(args, "subject"))
	case "eve_fitting_save":
		return fmt.Sprintf("save fitting '%s'", argString(args, "name"))
	case "eve_fitting_delete":
		return "delete fitting " + argString(args, "fitting_id")
	case "eve_contacts_set":
		return fmt.Sprintf("set standing on %d contacts", lenAny(args["contact_ids"], args["ids"]))
	case "eve_contacts_delete":
		return fmt.Sprintf("delete %d contacts", lenAny(args["contact_ids"], args["ids"]))
	case "eve_ui_set_waypoint":
		return "set waypoint " + argString(args, "destination_id")
	case "eve_ui_open_window":
		return fmt.Sprintf("open %s window", argString(args, "window"))
	case "eve_mail_mark":
		return "mark mail " + argString(args, "mail_id")
	case "eve_mail_delete":
		return "delete mail " + argString(args, "mail_id")
	case "eve_calendar_respond":
		return fmt.Sprintf("respond %s to event %s", argString(args, "response"), argString(args, "event_id"))
	case "eve_mail_compose":
		return fmt.Sprintf("open compose to %d recipients, subject '%s'",
			lenAny(args["recipients"], args["to"]), argString(args, "subject"))
	default:
		return tool
	}
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}

	return string([]rune(s)[:n])
}

func argString(args map[string]any, key string) string {
	v, ok := args[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		return strconv.FormatInt(int64(t), 10)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func lenAny(values ...any) int {
	for _, v := range values {
		switch t := v.(type) {
		case []any:
			if t != nil {
				return len(t)
			}
		case []map[string]any:
			if t != nil {
				return len(t)
			}
		case []string:
			if t != nil {
				return len(t)
			}
		case []int:
			if t != nil {
				return len(t)
			}
		}
	}

	return 0
}
