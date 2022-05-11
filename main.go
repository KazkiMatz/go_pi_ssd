package main

import (
  "fmt"
  "math"
  "os"
  "time"
  "strconv"
  "errors"
  "encoding/json"
  "github.com/stianeikeland/go-rpio"
)

type Display struct {
  triggerPinIds []int
  triggerPins []rpio.Pin
  segmentPins []rpio.Pin
  initPins []bool
  donePins []bool
  digits []int
  dpIdx int
  chars []int
}

func (d *Display) tryDetectPin(i int) bool {
  if d.donePins[i] {
    return false
  }

  //fmt.Println(fmt.Sprintf("GPIO Pins: %v, target: %d", d.triggerPins, d.triggerPins[i]))

  if d.triggerPins[i].Read() == rpio.Low {
    d.donePins[i] = true
    return true
  }

  return false
}

func (d *Display) done() bool {
  for i, _ := range d.donePins {
    if !d.donePins[i] {
      return false
    }
  }

  return true
}

func (d *Display) clear() {
  for i, _ := range d.donePins {
    d.initPins[i] = false
    d.donePins[i] = false
  }
}

func (d *Display) setDigit(i int, n int) {
  d.digits[i] = n
}

func (d *Display) setDP(i int, on bool) {
  //fmt.Println(fmt.Sprintf("%v %d %v", d.digits, i, on))
  if on {
    d.dpIdx = i
  } else {
    if d.dpIdx == i {
      d.dpIdx = len(d.digits)-1
    }
  }
}

func (d *Display) readSegment() (int, bool, error) {
  p := 0
  dp := false
  for i, pin := range d.segmentPins {
    if i == 7 {
      dp = int(pin.Read()) > 0
    } else {
      d := int(pin.Read()) << (6-i)
      p += d
    }
  }

  if p == 0 {
    return -1, dp, nil
  }

  for i, n := range d.chars {
    if n == p {
      return i, dp, nil
    }
  }

  return -2, dp, errors.New(fmt.Sprintf("Not readable: %b", p))
}

func (d *Display) readDigits() float64 {
  val := 0.0
  for i, seg := range d.digits {
    if seg < 0 {
      continue
    }

    if i > d.dpIdx {
      val = val + float64(seg)*math.Pow(10.0, float64(d.dpIdx - i))
    } else {
      val = val*10 + float64(seg)
    }
  }

  return val
}

var triggerLvEdgeDuration = 500*time.Microsecond
var triggerPinPollInterval = 100*time.Microsecond
var segDriveEdgeDuration = 2*time.Millisecond

func (d *Display) Read() (float64, error) {
  for {
    for j, pin := range d.triggerPins {
      triggered := d.tryDetectPin(j)
      if triggered {
        time.Sleep(triggerLvEdgeDuration*2)

        pin.Detect(rpio.NoEdge)

        seg, dp, err := d.readSegment()

        if err == nil {
          d.setDigit(j, seg)
          if dp {
            d.setDP(j, true)
          } else {
            d.setDP(j, false)
          }
        } else {
          return 0.0, errors.New(fmt.Sprintf("%d: %s\n", j, err.Error()))
        }

        if d.done() {
          d.clear()
          return d.readDigits(), nil
        }

        time.Sleep(segDriveEdgeDuration/2)
      } else {
        time.Sleep(triggerPinPollInterval)
      }
    }
  }
}

func NewDisplay(triggerPinIds []int, segmentPins []rpio.Pin) *Display {
  if len(segmentPins) != 8 {
    panic("Wrong pin arguments")
  }

  chars := make([]int, len(numPatterns))
  for i, pat := range numPatterns {
    n, _ := strconv.ParseInt(pat, 2, 0)
    chars[i] = int(n)
  }

  triggerPins := make([]rpio.Pin, len(triggerPinIds))
  for j, id := range triggerPinIds {
    triggerPins[j] = rpio.Pin(id)
    triggerPins[j].Input()
    triggerPins[j].PullOff()
  }

  display := &Display{
    triggerPinIds: triggerPinIds,
    triggerPins: triggerPins,
    segmentPins: segmentPins,
    digits: make([]int, len(triggerPins)),
    initPins: make([]bool, len(triggerPins)),
    donePins: make([]bool, len(triggerPins)),
    chars: chars,
  }
  display.clear()

  return display
}

// a b c d e f g
var numPatterns = []string{
  "1111110", // 0
  "0110000", // 1
  "1101101", // 2
  "1111001", // 3
  "0110011", // 4
  "1011011", // 5
  "1011111", // 6
  "1110000", // 7
  "1111111", // 8
  "1111011", // 9
}

type Message struct {
  Time string `json:"time"`
  Count int `json:"count"`
  Values []float64 `json:"values"`
}

func main() {
  fmt.Println("opening gpio")
  err := rpio.Open()
  if err != nil {
    panic(fmt.Sprint("unable to open gpio", err.Error()))
  }

  defer rpio.Close()

  // a b c d e f g DP
  segmentPinIds := []int{26, 19, 13, 6, 5, 22, 27, 17}
  segmentPins := make([]rpio.Pin, len(segmentPinIds))
  for i, id := range segmentPinIds {
    segmentPins[i] = rpio.Pin(id)
    segmentPins[i].Input()
    segmentPins[i].PullOff()
  }

  triggerPinIdSets := [][]int{[]int{21, 20, 16}, []int{25, 24, 23}}

  displays := make([]*Display, len(triggerPinIdSets))
  for i, _ := range triggerPinIdSets {
    displays[i] = NewDisplay(triggerPinIdSets[i], segmentPins)
  }

  chVal := make(chan []float64)
  //chErr := make(chan struct{})

  go func() {
    for {
      vals := make([]float64, len(displays))

      for i, d := range displays {
        vals[i], err = d.Read()
        if err != nil {
          fmt.Fprintf(os.Stderr, "%s", err.Error())
        }
      }

      chVal <- vals

      time.Sleep(1000 * time.Millisecond)
    }
  }()

  c := 0

  for {
    vals := <-chVal
    m := Message{Values: vals, Count: c}
    mJson, _ := json.Marshal(m)
    fmt.Println(string(mJson))
    c += 1
  }
}
