package main

import (
	"gorm.io/driver/mysql"
	"gorm.io/gen"
	"gorm.io/gorm"
)

func main() {
	g := gen.NewGenerator(gen.Config{
		OutPath: "pkg/dao",
		Mode:    gen.WithoutContext | gen.WithDefaultQuery | gen.WithQueryInterface, // generate mode
	})

	gormdb, _ := gorm.Open(
		mysql.Open(
			"comate_stack_rw:comate-stack-test123@tcp(10.27.156.151:8306)/agent_runtime?charset=utf8mb4&collation=utf8mb4_unicode_ci&parseTime=true&loc=Asia%2FShanghai&interpolateParams=true",
		),
	)
	g.UseDB(gormdb) // reuse your gorm db

	// 只生成指定的表
	g.ApplyBasic(
		g.GenerateModel("app"),
		g.GenerateModel("app_grant_scope"),
		g.GenerateModel("app_pvc_binding"),
		g.GenerateModel("app_pvc_instance"),
		g.GenerateModel("artifacts"),
		g.GenerateModel("cluster"),
		g.GenerateModel("cluster_node"),
		g.GenerateModel("host_sequence"),
		g.GenerateModel("image"),
		g.GenerateModel("image_preheat_job"),

		g.GenerateModel("image_preheat_record"),
		g.GenerateModel("sandbox_pvc_binding"),

		g.GenerateModel("sandbox_pvc_migration"),

		g.GenerateModel("sandbox_runtime"),
		g.GenerateModel("sandbox_runtime_history"),
		g.GenerateModel("sandbox_runtime_pool"),
		g.GenerateModel("scheduled_task"),
		g.GenerateModel("template"),

		// 添加更多表...
	)
	// Generate the code
	g.Execute()
}
