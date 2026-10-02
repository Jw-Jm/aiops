package corootcheck
import("bytes";"fmt";"text/template")
type CheckType int
const(CheckTypeEventBased CheckType=iota;CheckTypeItemBased;CheckTypeValueBased;CheckTypeManual)
type Status string
const(UNKNOWN Status="unknown";OK Status="ok";WARNING Status="warning")
type CheckUnit string
type StringSet []string
func(s StringSet)Len()int{return len(s)}
type CheckContext struct{items StringSet;count int64;value float32;unit CheckUnit;threshold float32}
func(c CheckContext)Value()float32{return c.value}
func(c CheckContext)Threshold()float32{return c.threshold}
func(c CheckContext)Count()int64{return c.count}
type Check struct{Status Status;Message string;Threshold float32;Unit CheckUnit;typ CheckType;messageTemplate string;items StringSet;count int64;value float32;fired bool}
func(ch *Check)SetStatus(status Status,format string,a ...any){ch.Status=status;ch.Message=fmt.Sprintf(format,a...)}
func EvaluateValue(value,threshold float32,template string)Check{ch:=Check{Status:OK,typ:CheckTypeValueBased,value:value,Threshold:threshold,messageTemplate:template};ch.Calc();return ch}

func (ch *Check) Calc() {
	switch ch.typ {
	case CheckTypeEventBased:
		if ch.count <= int64(ch.Threshold) {
			return
		}
	case CheckTypeItemBased:
		if ch.items.Len() == 0 {
			return
		}
	case CheckTypeValueBased:
		if ch.value <= ch.Threshold {
			return
		}
	case CheckTypeManual:
		if !ch.fired {
			return
		}
	default:
		return
	}
	t, err := template.New("").Parse(ch.messageTemplate)
	if err != nil {
		ch.SetStatus(UNKNOWN, "invalid template: %s", err)
		return
	}
	buf := &bytes.Buffer{}
	if err := t.Execute(buf, CheckContext{items: ch.items, count: ch.count, value: ch.value, unit: ch.Unit, threshold: ch.Threshold}); err != nil {
		ch.SetStatus(UNKNOWN, "failed to render message: %s", err)
		return
	}
	ch.SetStatus(WARNING, "%s", buf.String())
}
