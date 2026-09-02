module example.com/depws/a

go 1.24

require (
	example.com/depws/b v0.0.0
	example.com/ext v1.2.3
	example.com/pinned v1.4.0
	example.com/solo v1.4.0
)

replace example.com/old v1.0.0 => example.com/new v1.9.0

replace example.com/pinned v1.0.0 => example.com/patched v1.0.1

replace example.com/wild => example.com/fork v1.0.0

replace example.com/wild v1.0.0 => example.com/patch v1.0.1
