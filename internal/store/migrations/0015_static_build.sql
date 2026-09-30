-- What a front end needs before it can be served, and where its output lands.
--
-- The static builder copied a directory into a web server and called it a
-- build. For a repository that already holds its HTML that is right. For a
-- Vite, Create React App or Astro project — which is what most front ends are —
-- it is not: the directory it was told to serve does not exist until something
-- runs the build, and nothing did. The image came out holding the repository's
-- own source, and the page was blank.
--
-- build_command is what produces the output, empty when nothing has to run.
-- static_dir is the directory to serve, relative to the root being built. It
-- was detected all along and thrown away before it reached the build.
ALTER TABLE apps ADD COLUMN build_command TEXT NOT NULL DEFAULT '';
ALTER TABLE apps ADD COLUMN static_dir TEXT NOT NULL DEFAULT '';
