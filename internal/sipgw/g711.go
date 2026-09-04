package sipgw

func linearToMuLaw(sample int16) byte {
	value := int(sample)
	sign := byte(0)
	if value < 0 {
		sign, value = 0x80, -value
		if value > 32767 {
			value = 32767
		}
	}
	value += 132
	if value > 32635 {
		value = 32635
	}
	exponent := 7
	for mask := 0x4000; exponent > 0 && value&mask == 0; mask >>= 1 {
		exponent--
	}
	mantissa := (value >> (exponent + 3)) & 0x0f
	return ^(sign | byte(exponent<<4) | byte(mantissa))
}

func muLawToLinear(value byte) int16 {
	value = ^value
	magnitude := ((int(value)&0x0f)<<3 + 132) << ((value & 0x70) >> 4)
	magnitude -= 132
	if value&0x80 != 0 {
		return int16(-magnitude)
	}
	return int16(magnitude)
}

func pcmToPCMU(samples []int16) []byte {
	out := make([]byte, len(samples))
	for i, sample := range samples {
		out[i] = linearToMuLaw(sample)
	}
	return out
}

func pcmuToPCM(payload []byte) []int16 {
	out := make([]int16, len(payload))
	for i, encoded := range payload {
		out[i] = muLawToLinear(encoded)
	}
	return out
}
