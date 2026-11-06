fn main() {
    tauri_build::try_build(tauri_build::Attributes::new().app_manifest(
        tauri_build::AppManifest::new().commands(&["choose_bundle", "refresh_bundle"]),
    ))
    .expect("could not generate desktop permissions");
}
