// NXP i.MX6UL ARM clock control
// https://github.com/inversepath/tamago
//
// Copyright (c) F-Secure Corporation
// https://foundry.f-secure.com
//
// Use of this source code is governed by the license
// that can be found in the LICENSE file.
//
// +build tamago,arm

package imx6

import (
	"errors"
	"log"
	"unsafe"

	"github.com/inversepath/tamago/imx6/internal/reg"
)

const (
	OSC_FREQ = 24000000

	CCM_CACRR          int32 = 0x020c4010
	CCM_CACRR_ARM_PODF       = 0

	CCM_ANALOG_PLL_ARM                uint32 = 0x020c8000
	CCM_ANALOG_PLL_ARM_LOCK                  = 31
	CCM_ANALOG_PLL_ARM_BYPASS                = 16
	CCM_ANALOG_PLL_ARM_BYPASS_CLK_SRC        = 14
	CCM_ANALOG_PLL_ARM_DIV_SELECT            = 0

	PMU_REG_CORE           uint32 = 0x020c8140
	PMU_REG_CORE_REG2_TARG        = 18
	PMU_REG_CORE_REG0_TARG        = 0
)

// ARMCoreDiv returns the ARM core divider value
// (p665, 18.6.5 CCM Arm Clock Root Register, IMX6ULLRM).
func ARMCoreDiv() (div float32) {
	cacrr := (*uint32)(unsafe.Pointer(uintptr(CCM_CACRR)))
	return float32(reg.Get(cacrr, CCM_CACRR_ARM_PODF, 0b111) + 1)
}

// ARMPLLDiv returns the ARM PLL divider value
// (p714, 18.7.1 Analog ARM PLL control Register, IMX6ULLRM).
func ARMPLLDiv() (div float32) {
	pll := (*uint32)(unsafe.Pointer(uintptr(CCM_ANALOG_PLL_ARM)))
	return float32(reg.Get(pll, CCM_ANALOG_PLL_ARM_DIV_SELECT, 0b1111111)) / 2
}

// ARMFreq returns the ARM core frequency.
func ARMFreq() (hz uint32) {
	// (OSC_FREQ * (DIV_SELECT / 2)) / (ARM_PODF + 1)
	return uint32((OSC_FREQ * ARMPLLDiv()) / ARMCoreDiv())
}

func setOperatingPointIMX6ULL(uV uint32) {
	var reg0Targ uint32
	var reg2Targ uint32

	pmu := (*uint32)(unsafe.Pointer(uintptr(PMU_REG_CORE)))
	curTarg := reg.Get(pmu, PMU_REG_CORE_REG0_TARG, 0b11111)

	// p2456, 39.6.4 Digital Regulator Core Register, IMX6ULLRM
	if uV < 725000 {
		reg0Targ = 0b00000
	} else if uV > 1450000 {
		reg0Targ = 0b11111
	} else {
		reg0Targ = (uV - 700000) / 25000
	}

	if reg0Targ == curTarg {
		return
	}

	// VDD_SOC_CAP Min is 1150000 (targ == 18)
	if reg0Targ < 18 {
		reg2Targ = 18
	} else {
		reg2Targ = reg0Targ
	}

	log.Printf("imx6_clk: changing ARM core operating point to %d uV\n", reg0Targ*25000)

	r := *pmu

	// clear target voltages
	reg.ClearN(&r, 0, (0b11111<<PMU_REG_CORE_REG2_TARG | 0b11111<<PMU_REG_CORE_REG0_TARG))

	// set ARM core target voltage
	reg.SetN(&r, PMU_REG_CORE_REG0_TARG, 0b11111, reg0Targ)

	// set SOC target voltage
	reg.SetN(&r, PMU_REG_CORE_REG2_TARG, 0b11111, reg2Targ)

	*pmu = r
	busyloop(10000)

	log.Printf("imx6_clk: %d uV -> %d uV\n", curTarg*25000, reg0Targ*25000)
}

func setARMFreqIMX6ULL(hz uint32) (err error) {
	var div_select uint32
	var arm_podf uint32
	var uV uint32

	cacrr := (*uint32)(unsafe.Pointer(uintptr(CCM_CACRR)))
	pll := (*uint32)(unsafe.Pointer(uintptr(CCM_ANALOG_PLL_ARM)))
	curHz := ARMFreq()

	if hz == curHz {
		return
	}

	log.Printf("imx6_clk: changing ARM core frequency to %d MHz\n", hz/1000000)

	// p24, Table 10. Operating Ranges, IMX6ULLCEC
	switch hz {
	case 900000000:
		div_select = 75
		arm_podf = 0
		uV = 1275000
	case 792000000:
		div_select = 66
		arm_podf = 0
		uV = 1225000
	case 528000000:
		div_select = 88
		arm_podf = 1
		uV = 1175000
	case 396000000:
		div_select = 66
		arm_podf = 1
		uV = 1025000
	case 198000000:
		div_select = 66
		arm_podf = 3
		uV = 950000
	default:
		return errors.New("unsupported")
	}

	if hz > curHz {
		setOperatingPointIMX6ULL(uV)
	}

	// set bypass source to main oscillator
	reg.SetN(pll, CCM_ANALOG_PLL_ARM_BYPASS_CLK_SRC, 0b11, 0)

	// bypass
	reg.Set(pll, CCM_ANALOG_PLL_ARM_BYPASS)

	// set PLL divisor
	reg.SetN(pll, CCM_ANALOG_PLL_ARM_DIV_SELECT, 0b1111111, div_select)

	// wait for lock
	log.Printf("imx6_clk: waiting for PLL lock\n")
	reg.Wait(pll, CCM_ANALOG_PLL_ARM_LOCK, 0b1, 1)

	// remove bypass
	reg.Clear(pll, CCM_ANALOG_PLL_ARM_BYPASS)

	// set core divisor
	reg.SetN(cacrr, CCM_CACRR_ARM_PODF, 0b111, arm_podf)

	if hz < curHz {
		setOperatingPointIMX6ULL(uV)
	}

	log.Printf("imx6_clk: %d MHz -> %d MHz\n", curHz/1000000, hz/1000000)

	return
}

// SetARMFreq changes the ARM core frequency to the desired setting (in hertz).
func SetARMFreq(hz uint32) (err error) {
	switch Family {
	case IMX6ULL:
		err = setARMFreqIMX6ULL(hz)
	default:
		err = errors.New("unsupported")
	}

	return
}
