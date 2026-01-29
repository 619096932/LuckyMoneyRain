package game

type XorShift32 struct {
	state uint32
}

func NewXorShift32(seed uint32) *XorShift32 {
	if seed == 0 {
		seed = 0x12345678
	}
	return &XorShift32{state: seed}
}

func (x *XorShift32) Next() uint32 {
	s := x.state
	s ^= s << 13
	s ^= s >> 17
	s ^= s << 5
	x.state = s
	return s
}

func (x *XorShift32) Float64() float64 {
	const maxUint32 = float64(^uint32(0))
	return float64(x.Next()) / maxUint32
}

func shuffle(vals []int, rng *XorShift32) {
	for i := len(vals) - 1; i > 0; i-- {
		j := int(rng.Next() % uint32(i+1))
		vals[i], vals[j] = vals[j], vals[i]
	}
}
