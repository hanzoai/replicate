source = ["./dist/replicate"]
bundle_id = "ai.hanzo.replicate"

apple_id {
  username = "@env:APPLE_ID_USERNAME"
  password = "@env:AC_PASSWORD"
  provider = "@env:APPLE_TEAM_ID"
}

sign {
  application_identity = "@env:APPLE_DEVELOPER_ID_APPLICATION"
  entitlements_file = ""
}

notarize {
  path = "./dist/replicate.zip"
  bundle_id = "ai.hanzo.replicate"
  staple = true
}

zip {
  output_path = "./dist/replicate-signed.zip"
}
