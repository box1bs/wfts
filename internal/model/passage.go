package model

type Passage struct {
	Text string
	Type byte
}

type WordCountAndPositions struct {
	Count 		int
	Positions 	[]Position
}

type Position struct {
	I 		int
	Type 	byte
}

func NewTypeTextObj[T Passage | Position](t byte, text string, i int) T {
	switch t {
	case BodyType, HeaderType:

	default:
		panic("unnamed passage type")
	}

	switch any(*new(T)).(type) {
	case Passage:
		out := Passage{Text: text, Type: t}
		return any(out).(T)
	case Position:
		out := Position{I: i, Type: t}
		return any(out).(T)
	default:
		panic("unnamed passage type")
	}
}