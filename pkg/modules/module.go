package modules

type RandomStruct struct {
	Name string
}

func SomeModuleFunc() {
	otherMinorModuleFunc()
	someMoreFunc()
}

func otherMinorModuleFunc() {
	randomFunc()
}

func someMoreFunc() {

}

func randomFunc() {

}
