
 package main

 import (
     "bytes"
     "flag"
     "fmt"
     "go/ast"
     "go/format"
     "go/parser"
     "go/token"
     "io/ioutil"
     "os"
     "path/filepath"
     "sort"
     "strings"
 )

 // ConversionIssue represents a code pattern that needs manual attention
 type ConversionIssue struct {
     Filename  string
     Line      int
     Column    int
     IssueType string
     Message   string
     Code      string
 }

 // FileStats tracks statistics for a processed file
 type FileStats struct {
     AutomaticConversions int
     ManualConversions    int
     Issues               []ConversionIssue
 }

 // GlobalStats tracks global migration statistics
 type GlobalStats struct {
     FilesProcessed      int
     FilesModified       int
     AutomaticConversions int
     ManualConversions    int
     Issues              []ConversionIssue
 }

 // Tracks files that need imports added
 // TimeLiteral represents a time literal that needs to be replaced
 type TimeLiteral struct {
     Position token.Position
     OldCode  string
     NewCode  string
 }

 type fileImportTracker struct {
     needsMetav1Import map[string]bool
     hasTimeImport     map[string]bool
     fileStats         map[string]*FileStats
     globalStats       *GlobalStats
     fset              *token.FileSet
     dryRun            bool
     timeLiterals      map[string][]TimeLiteral
 }

 func newFileImportTracker(fset *token.FileSet, dryRun bool) *fileImportTracker {
     return &fileImportTracker{
         needsMetav1Import: make(map[string]bool),
         hasTimeImport:     make(map[string]bool),
         fileStats:         make(map[string]*FileStats),
         globalStats:       &GlobalStats{Issues: []ConversionIssue{}},
         fset:              fset,
         dryRun:            dryRun,
         timeLiterals:      make(map[string][]TimeLiteral),
     }
 }

 func (f *fileImportTracker) markNeedsImport(filename string) {
     f.needsMetav1Import[filename] = true
 }

 func (f *fileImportTracker) markHasTimeImport(filename string) {
     f.hasTimeImport[filename] = true
 }

 func (f *fileImportTracker) needsImport(filename string) bool {
     return f.needsMetav1Import[filename]
 }

 func (f *fileImportTracker) getFileStats(filename string) *FileStats {
     if _, exists := f.fileStats[filename]; !exists {
         f.fileStats[filename] = &FileStats{
             Issues: []ConversionIssue{},
         }
     }
     return f.fileStats[filename]
 }

 func (f *fileImportTracker) recordAutomaticConversion(filename string) {
     f.getFileStats(filename).AutomaticConversions++
     f.globalStats.AutomaticConversions++
 }

 func (f *fileImportTracker) recordManualConversion(filename string, node ast.Node, issueType, message, code string) {
     position := f.fset.Position(node.Pos())
     issue := ConversionIssue{
         Filename:  filename,
         Line:      position.Line,
         Column:    position.Column,
         IssueType: issueType,
         Message:   message,
         Code:      code,
     }

     f.getFileStats(filename).ManualConversions++
     f.getFileStats(filename).Issues = append(f.getFileStats(filename).Issues, issue)

     f.globalStats.ManualConversions++
     f.globalStats.Issues = append(f.globalStats.Issues, issue)
 }

 func (f *fileImportTracker) recordFileProcessed() {
     f.globalStats.FilesProcessed++
 }

 func (f *fileImportTracker) recordFileModified() {
     f.globalStats.FilesModified++
 }

 func (f *fileImportTracker) recordTimeLiteral(filename string, node ast.Node, oldCode, newCode string) {
     position := f.fset.Position(node.Pos())
     f.timeLiterals[filename] = append(f.timeLiterals[filename], TimeLiteral{
         Position: position,
         OldCode:  oldCode,
         NewCode:  newCode,
     })
 }

 func (f *fileImportTracker) getTimeLiterals(filename string) []TimeLiteral {
     return f.timeLiterals[filename]
 }

 func (f *fileImportTracker) generateReport() string {
     var report strings.Builder

     report.WriteString("\n=== Time to metav1 Migration Report ===\n\n")
     report.WriteString(fmt.Sprintf("Files processed: %d\n", f.globalStats.FilesProcessed))
     report.WriteString(fmt.Sprintf("Files modified: %d\n", f.globalStats.FilesModified))
     report.WriteString(fmt.Sprintf("Automatic conversions: %d\n", f.globalStats.AutomaticConversions))
     report.WriteString(fmt.Sprintf("Manual conversions needed: %d\n", f.globalStats.ManualConversions))

     if f.globalStats.ManualConversions > 0 {
         report.WriteString("\n=== Manual Conversion Issues ===\n\n")

         // Group issues by type
         issuesByType := make(map[string][]ConversionIssue)
         for _, issue := range f.globalStats.Issues {
             issuesByType[issue.IssueType] = append(issuesByType[issue.IssueType], issue)
         }

         // Sort issue types for consistent output
         issueTypes := make([]string, 0, len(issuesByType))
         for issueType := range issuesByType {
             issueTypes = append(issueTypes, issueType)
         }
         sort.Strings(issueTypes)

         for _, issueType := range issueTypes {
             issues := issuesByType[issueType]
             report.WriteString(fmt.Sprintf("== %s (%d issues) ==\n", issueType, len(issues)))

             for _, issue := range issues {
                 report.WriteString(fmt.Sprintf("  %s:%d:%d: %s\n", issue.Filename, issue.Line, issue.Column, issue.Message))
                 if issue.Code != "" {
                     report.WriteString(fmt.Sprintf("    Code: %s\n", issue.Code))
                 }
             }
             report.WriteString("\n")
         }

         report.WriteString("\n=== Conversion Guide ===\n\n")
         report.WriteString("1. time.Now() → metav1.Now()\n")
         report.WriteString("2. time.Time → metav1.Time\n")
         report.WriteString("3. time.Duration → metav1.Duration\n")
         report.WriteString("4. 5*time.Second → metav1.Duration{Duration: 5*time.Second}\n")
         report.WriteString("5. time.Since(t) → metav1.Now().Sub(t.Time) (if t is metav1.Time)\n")
         report.WriteString("6. time.Until(t) → t.Time.Sub(metav1.Now().Time) (if t is metav1.Time)\n")
         report.WriteString("7. time.Parse() → needs custom parsing and conversion to metav1.Time\n")
     }

     if f.dryRun {
         report.WriteString("\nThis was a dry run. No files were modified.\n")
     }

     return report.String()
 }

 func main() {
     // Parse command line flags
     dryRun := flag.Bool("dry-run", false, "Perform a dry run without modifying files")
     reportFile := flag.String("report", "", "Write report to file instead of stdout")
     flag.Parse()

     args := flag.Args()
     if len(args) < 1 {
         fmt.Println("Usage: go run migrate_time.go [options] <directory>")
         fmt.Println("Options:")
         flag.PrintDefaults()
         os.Exit(1)
     }

     rootDir := args[0]
     fset := token.NewFileSet()
     tracker := newFileImportTracker(fset, *dryRun)

     // Walk through all .go files in the directory
     err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
         if err != nil {
             return err
         }

         // Skip directories, non-Go files, and vendor directory
         if info.IsDir() {
             if info.Name() == "vendor" || info.Name() == ".git" {
                 return filepath.SkipDir
             }
             return nil
         }

         if !strings.HasSuffix(path, ".go") {
             return nil
         }

         // Process the file
         return processFile(path, fset, tracker)
     })

     if err != nil {
         fmt.Printf("Error walking directory: %v\n", err)
         os.Exit(1)
     }

     // Generate and output the report
     report := tracker.generateReport()

     if *reportFile != "" {
         err := ioutil.WriteFile(*reportFile, []byte(report), 0644)
         if err != nil {
             fmt.Printf("Error writing report to %s: %v\n", *reportFile, err)
             fmt.Println(report)
         } else {
             fmt.Printf("Report written to %s\n", *reportFile)
         }
     } else {
         fmt.Println(report)
     }
 }

 func processFile(filename string, fset *token.FileSet, tracker *fileImportTracker) error {
     // Read the file
     src, err := ioutil.ReadFile(filename)
     if err != nil {
         return fmt.Errorf("error reading file %s: %v", filename, err)
     }

     // Parse the file
     file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
     if err != nil {
         return fmt.Errorf("error parsing file %s: %v", filename, err)
     }

     tracker.recordFileProcessed()

     // Track if we made any changes
     modified := false

     // Process imports first
     hasMetav1Import := false
     var metav1ImportName string = "metav1"

     for _, imp := range file.Imports {
         if imp.Path.Value == `"time"` {
             tracker.markHasTimeImport(filename)
         }
         if strings.Contains(imp.Path.Value, `"k8s.io/apimachinery/pkg/apis/meta/v1"`) {
             hasMetav1Import = true
             if imp.Name != nil {
                 metav1ImportName = imp.Name.Name
             }
         }
     }

     // Process the AST
     ast.Inspect(file, func(n ast.Node) bool {
         if n == nil {
             return true
         }

         switch x := n.(type) {
         // Check for time.Duration, time.Time, time.Now(), etc.
         case *ast.SelectorExpr:
             if ident, ok := x.X.(*ast.Ident); ok && ident.Name == "time" {
                 switch x.Sel.Name {
                 case "Duration":
                     // Replace time.Duration with metav1.Duration
                     ident.Name = metav1ImportName
                     modified = true
                     tracker.recordAutomaticConversion(filename)
                     tracker.markNeedsImport(filename)
                 case "Time":
                     // Replace time.Time with metav1.Time
                     ident.Name = metav1ImportName
                     modified = true
                     tracker.recordAutomaticConversion(filename)
                     tracker.markNeedsImport(filename)
                 case "Now":
                     // Replace time.Now() with metav1.Now()
                     ident.Name = metav1ImportName
                     modified = true
                     tracker.recordAutomaticConversion(filename)
                     tracker.markNeedsImport(filename)
                 case "Since", "Until":
                     // These functions need special handling
                     var buf bytes.Buffer
                     format.Node(&buf, fset, n)
                     tracker.recordManualConversion(
                         filename,
                         n,
                         "Time Function",
                         fmt.Sprintf("time.%s needs manual conversion to metav1.Now().Sub/metav1.Time.Sub", x.Sel.Name),
                         buf.String(),
                     )
                     modified = true
                     tracker.recordAutomaticConversion(filename)
                     tracker.markNeedsImport(filename)
                 case "Parse", "ParseDuration", "ParseInLocation", "Unix", "UnixMilli", "UnixMicro", "UnixNano":
                     // These functions need special handling
                     var buf bytes.Buffer
                     format.Node(&buf, fset, n)
                     tracker.recordManualConversion(
                         filename,
                         n,
                         "Time Parsing",
                         fmt.Sprintf("time.%s needs manual conversion", x.Sel.Name),
                         buf.String(),
                     )
                 case "Sleep", "After", "Tick", "NewTicker", "NewTimer":
                     // These functions need special handling
                     var buf bytes.Buffer
                     format.Node(&buf, fset, n)
                     tracker.recordManualConversion(
                         filename,
                         n,
                         "Time Control",
                         fmt.Sprintf("time.%s has no metav1 equivalent", x.Sel.Name),
                         buf.String(),
                     )
                 case "Second", "Minute", "Hour", "Nanosecond", "Microsecond", "Millisecond":
                     // Record the original time constant for manual conversion
                     var buf bytes.Buffer
                     format.Node(&buf, fset, n)
                     
                     // Record this as a manual conversion with the suggested replacement
                     tracker.recordManualConversion(
                         filename,
                         n,
                         "Time Constant",
                         fmt.Sprintf("time.%s needs conversion to metav1.Duration", x.Sel.Name),
                         fmt.Sprintf("metav1.Duration{Duration: %s}", buf.String()),
                     )
                     
                     // Mark the file as modified so we add the metav1 import
                     modified = true
                     tracker.markNeedsImport(filename)
                 }
             }

         // Check for time literals like 5*time.Second
         case *ast.BinaryExpr:
             if x.Op == token.MUL {
                 if sel, ok := x.Y.(*ast.SelectorExpr); ok {
                     if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
                         // Check for time units
                         units := []string{"Nanosecond", "Microsecond", "Millisecond", "Second", "Minute", "Hour"}
                         for _, unit := range units {
                             if sel.Sel.Name == unit {
                                 // Get the original code
                                 var buf bytes.Buffer
                                 format.Node(&buf, fset, x)
                                 oldCode := buf.String()
                                 
                                 // Create the replacement code
                                 newCode := fmt.Sprintf("metav1.Duration{Duration: %s}", oldCode)
                                 
                                 // Record the time literal for replacement
                                 tracker.recordTimeLiteral(filename, x, oldCode, newCode)
                                 
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                                 break
                             }
                         }
                     }
                 }
             }

         // Check for variable declarations with time types
         case *ast.ValueSpec:
             for i, _ := range x.Names {
                 // Check if type is specified
                 if x.Type != nil {
                     if sel, ok := x.Type.(*ast.SelectorExpr); ok {
                         if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
                             if sel.Sel.Name == "Duration" {
                                 // Replace time.Duration with metav1.Duration
                                 ident.Name = metav1ImportName
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                             } else if sel.Sel.Name == "Time" {
                                 // Replace time.Time with metav1.Time
                                 ident.Name = metav1ImportName
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                             }
                         }
                     }
                 }

                 // Check if value is a time literal
                 if i < len(x.Values) {
                     if call, ok := x.Values[i].(*ast.CallExpr); ok {
                         if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
                             if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
                                 if sel.Sel.Name == "Now" {
                                     // Replace time.Now() with metav1.Now()
                                     ident.Name = metav1ImportName
                                     modified = true
                                     tracker.recordAutomaticConversion(filename)
                                     tracker.markNeedsImport(filename)
                                 } else if sel.Sel.Name == "Parse" || sel.Sel.Name == "ParseDuration" {
                                     if len(call.Args) > 0 {
                                         // Replace time.Parse() call with manual parsing
                                         call.Fun = &ast.SelectorExpr{
                                             X:   ast.NewIdent("metav1"),
                                             Sel: ast.NewIdent("ParseTime"),
                                         }
                                         modified = true
                                         tracker.recordManualConversion(
                                             filename,
                                             call,
                                             "Time Parsing",
                                             "time.Parse needs manual conversion to metav1.ParseTime",
                                             "",
                                         )
                                     } else {
                                         // time.Now() call
                                         call.Fun = &ast.SelectorExpr{
                                             X:   ast.NewIdent("metav1"),
                                             Sel: ast.NewIdent("Now"),
                                         }
                                         modified = true
                                         tracker.recordAutomaticConversion(filename)
                                         tracker.markNeedsImport(filename)
                                     }
                                 }
                             }
                         }
                     }
                 }
             }

         // Check for function parameters and return types
         case *ast.FuncDecl:
             if x.Type.Params != nil {
                 for _, field := range x.Type.Params.List {
                     if sel, ok := field.Type.(*ast.SelectorExpr); ok {
                         if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
                             if sel.Sel.Name == "Duration" {
                                 // Replace time.Duration with metav1.Duration
                                 ident.Name = metav1ImportName
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                             } else if sel.Sel.Name == "Time" {
                                 // Replace time.Time with metav1.Time
                                 ident.Name = metav1ImportName
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                             }
                         }
                     }
                 }
             }

             if x.Type.Results != nil {
                 for _, field := range x.Type.Results.List {
                     if sel, ok := field.Type.(*ast.SelectorExpr); ok {
                         if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
                             if sel.Sel.Name == "Duration" {
                                 // Replace time.Duration with metav1.Duration
                                 ident.Name = metav1ImportName
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                             } else if sel.Sel.Name == "Time" {
                                 // Replace time.Time with metav1.Time
                                 ident.Name = metav1ImportName
                                 modified = true
                                 tracker.recordAutomaticConversion(filename)
                                 tracker.markNeedsImport(filename)
                             }
                         }
                     }
                 }
             }

         // Check for struct fields
         case *ast.Field:
             if sel, ok := x.Type.(*ast.SelectorExpr); ok {
                 if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == "time" {
                     if sel.Sel.Name == "Duration" {
                         // Replace time.Duration with metav1.Duration
                         ident.Name = metav1ImportName
                         modified = true
                         tracker.recordAutomaticConversion(filename)
                         tracker.markNeedsImport(filename)
                     } else if sel.Sel.Name == "Time" {
                         // Replace time.Time with metav1.Time
                         ident.Name = metav1ImportName
                         modified = true
                         tracker.recordAutomaticConversion(filename)
                         tracker.markNeedsImport(filename)
                     }
                 }
             }
         }
         return true
     })

     // If we made changes and need to add the metav1 import
     if modified && tracker.needsImport(filename) && !hasMetav1Import {
         // Add metav1 import
         addMetav1Import(file)
         modified = true
     }

     // If we made changes, write the file back
     if modified {
         tracker.recordFileModified()

         if !tracker.dryRun {
             // Convert the AST to source code
             var buf bytes.Buffer
             if err := format.Node(&buf, fset, file); err != nil {
                 return fmt.Errorf("error formatting modified file %s: %v", filename, err)
             }
             
             // Get the source code as string
             src := buf.String()
             
             // Apply all time literal replacements
             timeLiterals := tracker.getTimeLiterals(filename)
             if len(timeLiterals) > 0 {
                 // Sort time literals by position in reverse order to avoid offset issues
                 sort.Slice(timeLiterals, func(i, j int) bool {
                     return timeLiterals[i].Position.Offset > timeLiterals[j].Position.Offset
                 })
                 
                 // Apply replacements
                 for _, tl := range timeLiterals {
                     src = strings.Replace(src, tl.OldCode, tl.NewCode, 1)
                 }
             }
             
             // Write the modified source back to the file
             if err := ioutil.WriteFile(filename, []byte(src), 0644); err != nil {
                 return fmt.Errorf("error writing modified file %s: %v", filename, err)
             }

             fmt.Printf("Modified: %s\n", filename)
         } else {
             fmt.Printf("Would modify: %s\n", filename)
         }
     }

     return nil
 }

 func addMetav1Import(file *ast.File) {
     // Create the import
     importSpec := &ast.ImportSpec{
         Path: &ast.BasicLit{
             Kind:  token.STRING,
             Value: `"k8s.io/apimachinery/pkg/apis/meta/v1"`,
         },
         Name: ast.NewIdent("metav1"),
     }

     // If there are existing imports, add to them
     if len(file.Imports) > 0 {
         // Find the import declaration
         var importDecl *ast.GenDecl
         for _, decl := range file.Decls {
             if genDecl, ok := decl.(*ast.GenDecl); ok && genDecl.Tok == token.IMPORT {
                 importDecl = genDecl
                 break
             }
         }

         if importDecl != nil {
             importDecl.Specs = append(importDecl.Specs, importSpec)
         } else {
             // Create a new import declaration
             file.Decls = append([]ast.Decl{
                 &ast.GenDecl{
                     Tok:   token.IMPORT,
                     Specs: []ast.Spec{importSpec},
                 },
             }, file.Decls...)
         }
     } else {
         // No existing imports, add a new import declaration
         file.Decls = append([]ast.Decl{
             &ast.GenDecl{
                 Tok:   token.IMPORT,
                 Specs: []ast.Spec{importSpec},
             },
         }, file.Decls...)
     }
 }

