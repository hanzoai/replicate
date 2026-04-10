source = ["./dist/replicate"]
bundle_id = "ai.hanzo.replicate"

apple_id {
  username = "@env:APPLE_ID"
  password = "@env:AC_PASSWORD"
}

sign {
  application_identity = "@env:APPLE_DEVELOPER_ID"
}

zip {
  output_path = "dist/replicate.zip"
}
